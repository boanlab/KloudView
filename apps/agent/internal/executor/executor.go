package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"time"

	"github.com/kloudview/kloudview/apps/agent/internal/client"
	"github.com/kloudview/kloudview/apps/agent/internal/inventory"
)

type Executor struct {
	allowedServices []string
	terminalUser    string
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.Buffer.Write(value)
	}
	return original, nil
}

func New(allowedServices []string) *Executor { return &Executor{allowedServices: allowedServices} }

func (e *Executor) WithTerminalUser(value string) *Executor {
	e.terminalUser = value
	return e
}

func (e *Executor) Run(operation client.Operation) (string, error) {
	switch operation.Type {
	case "inventory.refresh":
		result, err := json.Marshal(inventory.Collect())
		return string(result), err
	case "service.status", "service.restart":
		service := operation.Parameters["service"]
		if !slices.Contains(e.allowedServices, service) {
			return "", errors.New("service not allowed")
		}
		action := "is-active"
		if operation.Type == "service.restart" {
			action = "restart"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, "systemctl", action, service).CombinedOutput()
		return string(output), err
	case "logs.capture":
		return e.CaptureLogs(
			operation.Parameters["source"],
			operation.Parameters["since"],
			operation.Parameters["until"],
			operation.Parameters["priority"],
			atoiOr(operation.Parameters["lines"], 500),
		)
	default:
		return "", errors.New("operation not supported")
	}
}

func atoiOr(value string, fallback int) int {
	if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

func (e *Executor) RunTerminal(command string) (string, error) {
	if !terminalCommandAllowed(command) {
		return "", errors.New("command blocked by terminal policy")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, "/bin/sh", "-lc", command)
	process.Env = append(os.Environ(), "TERM=dumb")
	attr, extraEnv, err := e.terminalCredential()
	if err != nil {
		return "", err
	}
	if attr != nil {
		process.SysProcAttr = attr
		process.Env = append(process.Env, extraEnv...)
	}
	output := &limitedBuffer{limit: 1 << 20}
	process.Stdout = output
	process.Stderr = output
	err = process.Run()
	return output.String(), err
}
