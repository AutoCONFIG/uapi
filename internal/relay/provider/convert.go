package provider

import (
	"fmt"

	"github.com/AutoCONFIG/uapi/internal/relay/provider/convert"
	"github.com/AutoCONFIG/uapi/internal/relay/provider/ir"
)

func ConvertRequest(clientFormat, upstreamFormat Format, body []byte) ([]byte, error) {
	return convert.ConvertRequest(clientFormat, upstreamFormat, body)
}

func ConvertRequestDetailed(clientFormat, upstreamFormat Format, body []byte) ([]byte, *ir.Request, error) {
	return convert.ConvertRequestDetailed(clientFormat, upstreamFormat, body)
}

func NormalizeRequestSameProtocol(format Format, body []byte) ([]byte, error) {
	return convert.NormalizeRequestSameProtocol(format, body)
}

type credentialAwareAdaptor interface {
	SetCredentials(credentials string)
}

func ConvertRequestWithAdaptor(clientFormat, upstreamFormat Format, body []byte, adaptor Adaptor, credentials ...string) ([]byte, error) {
	req, err := convert.ToIR(clientFormat, body)
	if err != nil {
		return nil, fmt.Errorf("parse request %s: %w", clientFormat, err)
	}
	convert.PrepareRequestForTarget(req, clientFormat, upstreamFormat)
	if adaptor != nil {
		if aware, ok := adaptor.(credentialAwareAdaptor); ok && len(credentials) > 0 {
			aware.SetCredentials(credentials[0])
		}
		return adaptor.FromIR(req)
	}
	return convert.ConvertRequest(clientFormat, upstreamFormat, body)
}

func ConvertResponse(upstreamFormat, clientFormat Format, body []byte) ([]byte, error) {
	return convert.ConvertResponse(upstreamFormat, clientFormat, body)
}
