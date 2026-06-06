package gateway

import (
	"testing"
	"time"

	"github.com/AutoCONFIG/uapi/internal/channelcap"
	"github.com/google/uuid"
)

func TestPickRoutePrefersHigherChannelPriority(t *testing.T) {
	highPriorityNode := &nodeState{ID: uuid.New(), Weight: 1, Current: 20}
	lowPriorityNode := &nodeState{ID: uuid.New(), Weight: 1000}
	g := &Gateway{
		loadedAt: time.Now(),
		cacheTTL: time.Hour,
		nodes:    []*nodeState{lowPriorityNode, highPriorityNode},
		routes: []*routeCandidate{
			{
				Node:             lowPriorityNode,
				ChannelID:        uuid.New(),
				ChannelPriority:  10,
				ChannelWeight:    1000,
				AccountWeight:    1000,
				ChannelAPIFormat: "standard",
				ChannelType:      "openai",
				ChannelModels:    "glm-5.1",
			},
			{
				Node:             highPriorityNode,
				ChannelID:        uuid.New(),
				ChannelPriority:  90,
				ChannelWeight:    1,
				AccountWeight:    1,
				ChannelAPIFormat: "standard",
				ChannelType:      "openai",
				ChannelModels:    "glm-5.1",
			},
		},
	}

	route, release, ok := g.pickRoute("glm-5.1", channelcap.Request{})
	if !ok {
		t.Fatal("pickRoute returned no route")
	}
	defer release(false)
	if route.Node.ID != highPriorityNode.ID {
		t.Fatalf("selected node %s, want high priority node %s", route.Node.ID, highPriorityNode.ID)
	}
}

func TestPickRouteUsesNodeChannelAndAccountWeightsWithinPriority(t *testing.T) {
	lightNode := &nodeState{ID: uuid.New(), Weight: 1}
	heavyNode := &nodeState{ID: uuid.New(), Weight: 2}
	g := &Gateway{
		loadedAt: time.Now(),
		cacheTTL: time.Hour,
		nodes:    []*nodeState{lightNode, heavyNode},
		routes: []*routeCandidate{
			{
				Node:             lightNode,
				ChannelID:        uuid.New(),
				ChannelPriority:  50,
				ChannelWeight:    1,
				AccountWeight:    1,
				ChannelAPIFormat: "standard",
				ChannelType:      "openai",
				ChannelModels:    "gpt-4",
			},
			{
				Node:             heavyNode,
				ChannelID:        uuid.New(),
				ChannelPriority:  50,
				ChannelWeight:    3,
				AccountWeight:    5,
				ChannelAPIFormat: "standard",
				ChannelType:      "openai",
				ChannelModels:    "gpt-4",
			},
		},
	}

	route, release, ok := g.pickRoute("gpt-4", channelcap.Request{})
	if !ok {
		t.Fatal("pickRoute returned no route")
	}
	defer release(false)
	if route.Node.ID != heavyNode.ID {
		t.Fatalf("selected node %s, want weighted node %s", route.Node.ID, heavyNode.ID)
	}
}
