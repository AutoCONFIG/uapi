package types

import (
	"github.com/AutoCONFIG/cli-relay/internal/db"
	"github.com/valyala/fasthttp"
)

type Adaptor interface {
	Init(channel *db.Channel, account *db.Account)
	GetRequestURL(path string) (string, error)
	SetupRequestHeader(req *fasthttp.Request, credentials string) error
	ConvertRequest(body []byte) ([]byte, error)
	ParseUsage(respBody []byte) (promptTokens, completionTokens int, err error)
	ParseStreamUsage(lastChunk []byte) (promptTokens, completionTokens int, err error)
	GetChannelType() string
}
