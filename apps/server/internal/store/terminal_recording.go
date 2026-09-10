package store

import (
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

const terminalRecordingLimit = 1 << 20

func (s *Memory) AppendTerminalRecording(sessionID, targetID, direction, data string, now time.Time) domain.TerminalRecording {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, recording := range s.terminalRecordings {
		if now.After(recording.ExpiresAt) {
			delete(s.terminalRecordings, id)
		}
	}
	recording := s.terminalRecordings[sessionID]
	if recording.SessionID == "" {
		recording = domain.TerminalRecording{SessionID: sessionID, TargetID: targetID, CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	}
	remaining := terminalRecordingLimit - recording.Bytes
	if remaining <= 0 {
		recording.Truncated = true
		s.terminalRecordings[sessionID] = recording
		return recording
	}
	value := []byte(data)
	if len(value) > remaining {
		value = value[:remaining]
		recording.Truncated = true
	}
	recording.Events = append(recording.Events, domain.TerminalRecordingEvent{Sequence: len(recording.Events) + 1, Direction: direction, Data: string(value), Timestamp: now})
	recording.Bytes += len(value)
	recording.UpdatedAt = now
	s.terminalRecordings[sessionID] = recording
	return recording
}

func (s *Memory) TerminalRecording(sessionID string, now time.Time) (domain.TerminalRecording, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recording, ok := s.terminalRecordings[sessionID]
	if ok && now.After(recording.ExpiresAt) {
		delete(s.terminalRecordings, sessionID)
		return domain.TerminalRecording{}, false
	}
	return recording, ok
}

func (s *Memory) DeleteTerminalRecording(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.terminalRecordings[sessionID]; !ok {
		return ErrNotFound
	}
	delete(s.terminalRecordings, sessionID)
	return nil
}
