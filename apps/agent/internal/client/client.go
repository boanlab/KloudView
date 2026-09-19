package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/kloudview/kloudview/apps/agent/internal/inventory"
	"github.com/kloudview/kloudview/apps/agent/internal/logstream"
	"github.com/kloudview/kloudview/apps/agent/internal/metrics"
	"github.com/kloudview/kloudview/apps/agent/internal/selfupdate"
)

type Client struct {
	baseURL, enrollmentToken, credential, version string
	terminalEnabled, logStreamEnabled             bool
	http                                          *http.Client
}

const protocolVersion = "1"

type HTTPError struct {
	StatusCode int
	Status     string
}

func (e *HTTPError) Error() string {
	return "server status: " + e.Status
}

func RequiresEnrollment(err error) bool {
	var responseError *HTTPError
	return errors.As(err, &responseError) && (responseError.StatusCode == http.StatusUnauthorized || responseError.StatusCode == http.StatusNotFound)
}

type Operation struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	TargetIDs  []string          `json:"targetIds"`
	Parameters map[string]string `json:"parameters"`
}

type TerminalCommand struct {
	ID      string `json:"id"`
	Command string `json:"command"`
}

type enrollmentResponse struct {
	Agent struct {
		ID     string `json:"id"`
		NodeID string `json:"nodeId"`
	} `json:"agent"`
	Credential string `json:"credential"`
}

func New(baseURL, token, version string, terminalEnabled, logStreamEnabled bool) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), enrollmentToken: token, version: version, terminalEnabled: terminalEnabled, logStreamEnabled: logStreamEnabled, http: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) SetCredential(credential string) {
	c.credential = credential
}

func (c *Client) Credential() string {
	return c.credential
}

// capabilities is what the fleet view reports this agent is collecting, so it
// names every collection that is running.
func (c *Client) capabilities() []string {
	capabilities := []string{"inventory", "metrics"}
	if c.terminalEnabled {
		capabilities = append(capabilities, "terminal")
	}
	if c.logStreamEnabled {
		capabilities = append(capabilities, "logs")
	}
	return capabilities
}

func (c *Client) Enroll(host inventory.Host) (string, string, error) {
	payload := map[string]any{"token": c.enrollmentToken, "hostname": host.Hostname, "version": c.version, "protocolVersion": protocolVersion, "capabilities": c.capabilities(), "labels": map[string]string{"os": host.OS, "arch": host.Arch}}
	var response enrollmentResponse
	if err := c.doWithToken(http.MethodPost, "/api/v1/agents/enroll", payload, &response, ""); err != nil {
		return "", "", err
	}
	c.credential = response.Credential
	return response.Agent.ID, response.Agent.NodeID, nil
}

// HeartbeatResponse carries the build the server wants this agent to run.
type HeartbeatResponse struct {
	TargetVersion string               `json:"targetVersion"`
	Releases      []selfupdate.Release `json:"releases"`
	Credential    string               `json:"credential"`
}

func (c *Client) Heartbeat(agentID, nodeID string, host inventory.Host) (HeartbeatResponse, error) {
	payload := map[string]any{"nodeId": nodeID, "hostname": host.Hostname, "version": c.version, "protocolVersion": protocolVersion, "capabilities": c.capabilities(), "labels": map[string]string{"os": host.OS, "arch": host.Arch}}
	var response HeartbeatResponse
	err := c.do(http.MethodPost, "/api/v1/agents/"+agentID+"/heartbeat", payload, &response)
	return response, err
}

// DownloadRelease fetches a build; the caller verifies its checksum.
func (c *Client) DownloadRelease(release selfupdate.Release) (io.ReadCloser, error) {
	request, err := http.NewRequest(http.MethodGet, c.baseURL+release.URL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.credential)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("release download failed: %s", response.Status)
	}
	return response.Body, nil
}

func (c *Client) Metric(agentID, nodeID string, sample metrics.Sample) error {
	payload := map[string]any{"resourceId": nodeID, "timestamp": time.Now().UTC(), "cpu": sample.CPU, "memory": sample.Memory, "disk": sample.Disk, "networkRx": sample.NetworkRx, "networkTx": sample.NetworkTx}
	return c.do(http.MethodPost, "/api/v1/agents/"+agentID+"/metrics", payload, nil)
}

// ContainerMetrics ships per-container cgroup readings. The server owns the
// mapping from a runtime ID to its resource, so only the ID is sent.
func (c *Client) ContainerMetrics(agentID, nodeID string, stats map[string]inventory.ContainerStats) error {
	if len(stats) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(stats))
	for id, stat := range stats {
		items = append(items, map[string]any{"id": id, "cpuPercent": stat.CPUPercent, "memoryBytes": stat.MemoryBytes, "memoryLimitBytes": stat.MemoryLimitBytes, "memoryPercent": stat.MemoryPercent, "diskReadBytes": stat.DiskReadBytes, "diskWriteBytes": stat.DiskWriteBytes, "processes": stat.Processes})
	}
	payload := map[string]any{"nodeId": nodeID, "timestamp": time.Now().UTC(), "items": items}
	return c.do(http.MethodPost, "/api/v1/agents/"+agentID+"/container-metrics", payload, nil)
}

// VirtualMachineMetrics ships one reading per domain. The server derives the
// resource ID from the domain name, the same way the inventory does when it
// creates the resource.
func (c *Client) VirtualMachineMetrics(agentID, nodeID string, stats map[string]inventory.VMStats) error {
	if len(stats) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(stats))
	for name, stat := range stats {
		items = append(items, map[string]any{
			"name": name, "state": stat.State, "vcpus": stat.VCPUs,
			"cpuPercent": stat.CPUPercent, "memoryBytes": stat.MemoryBytes,
			"memoryLimitBytes": stat.MemoryLimitBytes, "memoryPercent": stat.MemoryPercent,
			"hostMemoryBytes": stat.HostMemoryBytes,
			"diskReadBytes":   stat.DiskReadBytes, "diskWriteBytes": stat.DiskWriteBytes,
			"networkRxRate": stat.NetworkRxRate, "networkTxRate": stat.NetworkTxRate,
		})
	}
	payload := map[string]any{"nodeId": nodeID, "timestamp": time.Now().UTC(), "items": items}
	return c.do(http.MethodPost, "/api/v1/agents/"+agentID+"/vm-metrics", payload, nil)
}

// ProcessMetrics ships one reading per sampled process. The server derives the
// resource ID from the process ID, the same way the inventory does when it
// creates the resource.
func (c *Client) ProcessMetrics(agentID, nodeID string, stats []inventory.ProcessStats) error {
	if len(stats) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(stats))
	for _, stat := range stats {
		items = append(items, map[string]any{
			"pid": stat.PID, "name": stat.Name, "state": stat.State,
			"cpuPercent": stat.CPUPercent, "memoryBytes": stat.MemoryBytes,
			"memoryPercent": stat.MemoryPercent, "hostMemoryBytes": stat.HostMemoryBytes,
			"threads": stat.Threads, "startedAt": stat.StartedAt,
		})
	}
	payload := map[string]any{"nodeId": nodeID, "timestamp": time.Now().UTC(), "items": items}
	return c.do(http.MethodPost, "/api/v1/agents/"+agentID+"/process-metrics", payload, nil)
}

// Logs ships one reporting window: severity counters for every level and the
// lines worth reading later.
func (c *Client) Logs(agentID, nodeID string, batch logstream.Batch) error {
	payload := map[string]any{"nodeId": nodeID, "from": batch.From, "to": batch.To, "counters": batch.Counters, "dropped": batch.Dropped, "lines": batch.Lines}
	return c.do(http.MethodPost, "/api/v1/agents/"+agentID+"/logs", payload, nil)
}

func (c *Client) Inventory(agentID string, host inventory.Host) error {
	return c.do(http.MethodPost, "/api/v1/agents/"+agentID+"/inventory", host, nil)
}

func (c *Client) ClaimOperation(agentID string) (*Operation, error) {
	request, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/agents/"+agentID+"/operations/claim", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.credential)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, &HTTPError{StatusCode: response.StatusCode, Status: response.Status}
	}
	var operation Operation
	if err := json.NewDecoder(response.Body).Decode(&operation); err != nil {
		return nil, err
	}
	return &operation, nil
}

func (c *Client) CompleteOperation(agentID, operationID, status, result, operationError string) error {
	payload := map[string]string{"status": status, "result": result, "error": operationError}
	return c.do(http.MethodPut, "/api/v1/agents/"+agentID+"/operations/"+operationID, payload, nil)
}

func (c *Client) ClaimTerminalCommand(agentID string) (*TerminalCommand, error) {
	request, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/agents/"+agentID+"/terminal/claim", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.credential)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, &HTTPError{StatusCode: response.StatusCode, Status: response.Status}
	}
	var command TerminalCommand
	if err := json.NewDecoder(response.Body).Decode(&command); err != nil {
		return nil, err
	}
	return &command, nil
}

func (c *Client) CompleteTerminalCommand(agentID, commandID, status, output, commandError string) error {
	payload := map[string]string{"status": status, "output": output, "error": commandError}
	return c.do(http.MethodPut, "/api/v1/agents/"+agentID+"/terminal/commands/"+commandID, payload, nil)
}

func (c *Client) TerminalStream(ctx context.Context, agentID string, serve func(context.Context, *websocket.Conn) error) error {
	streamURL := strings.Replace(c.baseURL, "http://", "ws://", 1)
	streamURL = strings.Replace(streamURL, "https://", "wss://", 1)
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.credential)
	conn, response, err := websocket.Dial(ctx, streamURL+"/api/v1/agents/"+agentID+"/terminal/stream", &websocket.DialOptions{HTTPHeader: header})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "agent stopped")
	return serve(ctx, conn)
}

func (c *Client) do(method, path string, payload, target any) error {
	return c.doWithToken(method, path, payload, target, c.credential)
}

func (c *Client) doWithToken(method, path string, payload, target any, token string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &HTTPError{StatusCode: response.StatusCode, Status: response.Status}
	}
	if target != nil {
		return json.NewDecoder(response.Body).Decode(target)
	}
	return nil
}
