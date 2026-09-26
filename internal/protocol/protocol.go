// Package protocol is the host side of core's Platform.GameStream.Protocol
// (apps/platform/lib/platform/game_stream/protocol.ex, task 01a0db5f stage 2).
// That moduledoc is the contract; this package mirrors it and nothing more.
//
// Core -> host (broadcasts on runtime:<runtime_id>):
//
//	play_session_start  {session_id, task_id?, repo_url, branch, sha, encoder, ice_servers, idle_timeout_s}
//	play_session_stop   {session_id, reason}
//	play_signal         {session_id, kind: answer|ice, data}
//
// Host -> core (pushes on the same topic):
//
//	play_session_status {session_id, state: building|connecting|live|ended|failed, detail, elapsed_ms}
//	play_signal         {session_id, kind: offer|ice, data}
//
// Unknown keys are ignored in both directions (the multi-player follow-on,
// task 01a0dbd6, widens play_signal with an optional slot key).
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/Startup-Suite/play-host/internal/media"
)

// Event names on the runtime channel.
const (
	EventSessionStart  = "play_session_start"
	EventSessionStop   = "play_session_stop"
	EventSignal        = "play_signal"
	EventSessionStatus = "play_session_status"
)

// Session states, in order. Ended and failed are terminal.
const (
	StateBuilding   = "building"
	StateConnecting = "connecting"
	StateLive       = "live"
	StateEnded      = "ended"
	StateFailed     = "failed"
)

// Signal kinds.
const (
	KindOffer  = "offer"  // host -> viewer
	KindAnswer = "answer" // viewer -> host
	KindICE    = "ice"    // both
)

// MaxDetailBytes is core's @max_detail_bytes. Core truncates rather than
// rejects; the host truncates first so what it logs is what core shows.
const MaxDetailBytes = 500

// MaxSignalBytes is core's @max_signal_bytes on the JSON-encoded data.
const MaxSignalBytes = 64 * 1024

// DefaultIdleTimeoutS is core's @default_idle_timeout_s.
const DefaultIdleTimeoutS = 600

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ICEServer is an RTCIceServer-shaped map. urls may be a string or a list.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// UnmarshalJSON accepts "urls" as either a string or a list of strings.
func (s *ICEServer) UnmarshalJSON(b []byte) error {
	var raw struct {
		URLs       json.RawMessage `json:"urls"`
		Username   string          `json:"username"`
		Credential string          `json:"credential"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	s.Username, s.Credential = raw.Username, raw.Credential
	var one string
	if err := json.Unmarshal(raw.URLs, &one); err == nil {
		s.URLs = []string{one}
		return nil
	}
	return json.Unmarshal(raw.URLs, &s.URLs)
}

// SessionStart is play_session_start.
type SessionStart struct {
	SessionID    string       `json:"session_id"`
	TaskID       string       `json:"task_id,omitempty"`
	RepoURL      string       `json:"repo_url"`
	Branch       string       `json:"branch"`
	SHA          string       `json:"sha"`
	Encoder      media.Preset `json:"encoder"`
	ICEServers   []ICEServer  `json:"ice_servers"`
	IdleTimeoutS int          `json:"idle_timeout_s"`
}

// DecodeSessionStart parses and validates a start payload. Encoder fields
// core leaves out keep the stage-1 control values.
func DecodeSessionStart(b []byte) (SessionStart, error) {
	s := SessionStart{Encoder: media.ControlPreset()}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("play_session_start: %w", err)
	}
	if s.SessionID == "" {
		return s, errors.New("play_session_start: missing session_id")
	}
	if !shaRe.MatchString(s.SHA) {
		return s, fmt.Errorf("play_session_start: sha %q is not 40 lowercase hex", s.SHA)
	}
	if s.RepoURL == "" {
		return s, errors.New("play_session_start: missing repo_url")
	}
	if s.IdleTimeoutS <= 0 {
		s.IdleTimeoutS = DefaultIdleTimeoutS
	}
	if err := s.Encoder.Validate(); err != nil {
		return s, fmt.Errorf("play_session_start: encoder: %w", err)
	}
	return s, nil
}

// SessionStop is play_session_stop.
type SessionStop struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
}

// Signal is play_signal in either direction. Data is relayed opaquely by
// core; its shape is agreed between host and viewer (see OfferData,
// AnswerData, ICEData).
type Signal struct {
	SessionID string          `json:"session_id"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
}

// SessionDescription is the data of an offer or answer: an
// RTCSessionDescriptionInit.
type SessionDescription struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

// ICECandidate is the data of an ice signal: an RTCIceCandidateInit. A null
// data (or an empty candidate) is end-of-candidates.
type ICECandidate struct {
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid,omitempty"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex,omitempty"`
	UsernameFragment *string `json:"usernameFragment,omitempty"`
}

// Description reads an offer/answer's data. A bare JSON string is taken as
// the SDP itself.
func (s Signal) Description() (SessionDescription, error) {
	var d SessionDescription
	var bare string
	if json.Unmarshal(s.Data, &bare) == nil && bare != "" {
		return SessionDescription{Type: s.Kind, SDP: bare}, nil
	}
	if err := json.Unmarshal(s.Data, &d); err != nil {
		return d, fmt.Errorf("%s data: %w", s.Kind, err)
	}
	if d.SDP == "" {
		return d, fmt.Errorf("%s data has no sdp", s.Kind)
	}
	if d.Type == "" {
		d.Type = s.Kind
	}
	return d, nil
}

// Candidate reads an ice signal's data. ok is false for end-of-candidates.
func (s Signal) Candidate() (c ICECandidate, ok bool, err error) {
	if len(s.Data) == 0 || string(s.Data) == "null" {
		return c, false, nil
	}
	if err := json.Unmarshal(s.Data, &c); err != nil {
		return c, false, fmt.Errorf("ice data: %w", err)
	}
	return c, c.Candidate != "", nil
}

// NewOfferSignal builds the host's offer.
func NewOfferSignal(sessionID, sdp string) (Signal, error) {
	data, err := json.Marshal(SessionDescription{Type: KindOffer, SDP: sdp})
	if err != nil {
		return Signal{}, err
	}
	if len(data) > MaxSignalBytes {
		return Signal{}, fmt.Errorf("offer is %d bytes, over core's %d cap", len(data), MaxSignalBytes)
	}
	return Signal{SessionID: sessionID, Kind: KindOffer, Data: data}, nil
}

// Status is play_session_status.
type Status struct {
	SessionID string `json:"session_id"`
	State     string `json:"state"`
	Detail    string `json:"detail"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

// NewStatus builds a status, truncating detail to core's cap on a rune
// boundary and clamping elapsed to >= 0 (core rejects a negative one).
func NewStatus(sessionID, state, detail string, elapsedMs int64) Status {
	return Status{SessionID: sessionID, State: state, Detail: TruncateDetail(detail), ElapsedMs: max(elapsedMs, 0)}
}

// Terminal reports whether state is ended or failed.
func Terminal(state string) bool { return state == StateEnded || state == StateFailed }

// TruncateDetail cuts d to at most MaxDetailBytes without splitting a rune.
func TruncateDetail(d string) string {
	if len(d) <= MaxDetailBytes {
		return d
	}
	cut := MaxDetailBytes
	for cut > 0 && !utf8.RuneStart(d[cut]) {
		cut--
	}
	return d[:cut]
}
