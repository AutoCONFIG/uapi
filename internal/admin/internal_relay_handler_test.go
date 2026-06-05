package admin

import (
	"testing"

	"github.com/valyala/fasthttp"
)

func TestUsageEventShouldSettleClientClosedWithUsage(t *testing.T) {
	req := UsageEventRequest{
		StatusCode:       499,
		PromptTokens:     120,
		CompletionTokens: 40,
	}

	if usageEventShouldRefund(req) {
		t.Fatal("499 usage events with parsed tokens should settle instead of refunding pre-consume")
	}
}

func TestUsageEventShouldRefundClientClosedWithoutUsage(t *testing.T) {
	req := UsageEventRequest{StatusCode: 499}

	if !usageEventShouldRefund(req) {
		t.Fatal("499 usage events without tokens should refund pre-consume")
	}
}

func TestUsageEventShouldSettleSuccessfulWithoutUsage(t *testing.T) {
	req := UsageEventRequest{StatusCode: fasthttp.StatusOK}

	if usageEventShouldRefund(req) {
		t.Fatal("successful usage events should settle through billing even when usage needs estimation fallback")
	}
}
