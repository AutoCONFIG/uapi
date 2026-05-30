package ir

import "encoding/json"

type LossSeverity string

const (
	LossInfo    LossSeverity = "info"
	LossWarning LossSeverity = "warning"
	LossError   LossSeverity = "error"
)

type Loss struct {
	SourceProtocol Protocol        `json:"source_protocol,omitempty"`
	TargetProtocol Protocol        `json:"target_protocol,omitempty"`
	Path           string          `json:"path,omitempty"`
	TargetPath     string          `json:"target_path,omitempty"`
	Field          string          `json:"field,omitempty"`
	Kind           string          `json:"kind,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Severity       LossSeverity    `json:"severity,omitempty"`
	ValueHash      string          `json:"value_hash,omitempty"`
	Preserved      bool            `json:"preserved,omitempty"`
	Native         json.RawMessage `json:"native,omitempty"`
}

func NewLoss(source, target Protocol, path, field, reason string, severity LossSeverity) Loss {
	return Loss{
		SourceProtocol: source,
		TargetProtocol: target,
		Path:           path,
		Field:          field,
		Reason:         reason,
		Severity:       severity,
	}
}
