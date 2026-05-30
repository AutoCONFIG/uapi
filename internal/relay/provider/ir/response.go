package ir

import "encoding/json"

type Response struct {
	SourceProtocol Protocol                   `json:"source_protocol,omitempty"`
	TargetProtocol Protocol                   `json:"target_protocol,omitempty"`
	ID             string                     `json:"id,omitempty"`
	Model          string                     `json:"model,omitempty"`
	Choices        []Choice                   `json:"choices,omitempty"`
	Usage          *Usage                     `json:"usage,omitempty"`
	Metadata       map[string]json.RawMessage `json:"metadata,omitempty"`
	Native         NativeEnvelope             `json:"native,omitempty"`
	Losses         []Loss                     `json:"losses,omitempty"`
}

type Choice struct {
	Index  int            `json:"index,omitempty"`
	Role   Role           `json:"role,omitempty"`
	Items  []Item         `json:"items,omitempty"`
	Finish *Finish        `json:"finish,omitempty"`
	Native NativeEnvelope `json:"native,omitempty"`
	Losses []Loss         `json:"losses,omitempty"`
}
