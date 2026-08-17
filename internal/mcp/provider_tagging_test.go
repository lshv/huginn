package mcp_test

import (
	"context"
	"testing"

	"github.com/scrypster/huginn/internal/mcp"
	"github.com/scrypster/huginn/internal/tools"
)

// Two MCP servers, each exposing a distinct tool, mirroring the deployed
// "zammad" (shared) and "zammad-tyr" (identity-scoped) pair.
func twoServerManager(t *testing.T) (*mcp.ServerManager, *tools.Registry) {
	t.Helper()

	byServer := map[string][]mcp.MCPTool{
		"zammad":     {{Name: "add_article"}, {Name: "create_ticket"}},
		"zammad-tyr": {{Name: "tyr_add_article"}, {Name: "tyr_create_ticket"}},
	}

	m := mcp.NewServerManager(
		[]mcp.MCPServerConfig{
			{Name: "zammad", Command: "x"},
			{Name: "zammad-tyr", Command: "x"},
		},
		mcp.WithClientFactory(func(ctx context.Context, cfg mcp.MCPServerConfig) (*mcp.MCPClient, []mcp.MCPTool, error) {
			return &mcp.MCPClient{}, byServer[cfg.Name], nil
		}),
	)
	reg := tools.NewRegistry()
	m.StartAll(context.Background(), reg)
	return m, reg
}

// StartAll must tag each tool with its own server name. Untagged tools (provider
// "") are reachable only through the "*" allow-all branch of
// AllSchemasForProviders, which is what made agent toolbelts all-or-nothing and
// let one agent call another agent's identity-scoped tools.
func TestStartAll_TagsToolsWithServerName(t *testing.T) {
	_, reg := twoServerManager(t)

	for name, want := range map[string]string{
		"add_article":       "zammad",
		"create_ticket":     "zammad",
		"tyr_add_article":   "zammad-tyr",
		"tyr_create_ticket": "zammad-tyr",
	} {
		if got := reg.ProviderFor(name); got != want {
			t.Errorf("ProviderFor(%q) = %q, want %q", name, got, want)
		}
	}
}

// The containment property itself: scoping a toolbelt to one server must yield
// that server's tools and none of the other's.
func TestToolbeltScoping_IsolatesServers(t *testing.T) {
	_, reg := twoServerManager(t)

	names := func(providers []string) map[string]bool {
		out := map[string]bool{}
		for _, s := range reg.AllSchemasForProviders(providers) {
			out[s.Function.Name] = true
		}
		return out
	}

	shared := names([]string{"zammad"})
	if !shared["add_article"] || !shared["create_ticket"] {
		t.Errorf("provider \"zammad\" lost its own tools: %v", shared)
	}
	if shared["tyr_create_ticket"] || shared["tyr_add_article"] {
		t.Errorf("provider \"zammad\" leaked zammad-tyr tools: %v", shared)
	}

	tyr := names([]string{"zammad-tyr"})
	if !tyr["tyr_create_ticket"] || !tyr["tyr_add_article"] {
		t.Errorf("provider \"zammad-tyr\" lost its own tools: %v", tyr)
	}
	if tyr["create_ticket"] || tyr["add_article"] {
		t.Errorf("provider \"zammad-tyr\" leaked shared zammad tools: %v", tyr)
	}

	// "*" must still mean everything, so existing configs keep working until
	// their toolbelts are narrowed.
	if all := names([]string{"*"}); len(all) != 4 {
		t.Errorf("wildcard should expose all 4 tools, got %d: %v", len(all), all)
	}
}
