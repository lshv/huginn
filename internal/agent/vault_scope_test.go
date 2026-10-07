package agent

import (
	"context"
	"testing"

	"github.com/scrypster/huginn/internal/agents"
	"github.com/scrypster/huginn/internal/backend"
	"github.com/scrypster/huginn/internal/tools"
)

// recordingVaultTool captures the args it was executed with so tests can assert
// which vault a call was routed to.
type recordingVaultTool struct {
	name     string
	lastArgs map[string]any
}

func (t *recordingVaultTool) Name() string                      { return t.name }
func (t *recordingVaultTool) Description() string               { return "test vault tool" }
func (t *recordingVaultTool) Permission() tools.PermissionLevel { return tools.PermWrite }
func (t *recordingVaultTool) Schema() backend.Tool {
	return backend.Tool{
		Type: "function",
		Function: backend.ToolFunction{
			Name:        t.name,
			Description: "test vault tool",
			Parameters: backend.ToolParameters{
				Type:       "object",
				Properties: map[string]backend.ToolProperty{},
			},
		},
	}
}
func (t *recordingVaultTool) Execute(_ context.Context, args map[string]any) tools.ToolResult {
	t.lastArgs = args
	return tools.ToolResult{Output: `{"ok":true}`}
}

func execTool(t *testing.T, reg *tools.Registry, name string, args map[string]any) {
	t.Helper()
	tool, ok := reg.Get(name)
	if !ok {
		t.Fatalf("tool %q not registered", name)
	}
	tool.Execute(context.Background(), args)
}

// TestPinAmbientVaultTools_InjectsVaultWhenAbsent is the core regression guard:
// an ambient muninn_* tool called without a "vault" argument must be routed to
// the agent's vault, not the MCP server's default.
func TestPinAmbientVaultTools_InjectsVaultWhenAbsent(t *testing.T) {
	reg := tools.NewRegistry()
	rec := &recordingVaultTool{name: "muninn_remember"}
	reg.Register(rec)

	if n := pinAmbientVaultTools(reg, "huginn-agent-heimdall"); n != 1 {
		t.Fatalf("expected 1 tool pinned, got %d", n)
	}

	execTool(t, reg, "muninn_remember", map[string]any{"concept": "Health Check"})

	if got := rec.lastArgs["vault"]; got != "huginn-agent-heimdall" {
		t.Errorf("expected vault to be injected as %q, got %v", "huginn-agent-heimdall", got)
	}
	if got := rec.lastArgs["concept"]; got != "Health Check" {
		t.Errorf("original args must be preserved, got concept=%v", got)
	}
}

// TestPinAmbientVaultTools_RespectsExplicitVault verifies an explicit vault
// argument always wins over the pinned default.
func TestPinAmbientVaultTools_RespectsExplicitVault(t *testing.T) {
	reg := tools.NewRegistry()
	rec := &recordingVaultTool{name: "muninn_recall"}
	reg.Register(rec)
	pinAmbientVaultTools(reg, "huginn-agent-heimdall")

	execTool(t, reg, "muninn_recall", map[string]any{"vault": "shared"})

	if got := rec.lastArgs["vault"]; got != "shared" {
		t.Errorf("explicit vault must win, got %v", got)
	}
}

// TestPinAmbientVaultTools_DoesNotMutateCallerArgs guards against the wrapper
// writing into a map the caller may reuse across calls.
func TestPinAmbientVaultTools_DoesNotMutateCallerArgs(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&recordingVaultTool{name: "muninn_remember"})
	pinAmbientVaultTools(reg, "vault-a")

	callerArgs := map[string]any{"concept": "x"}
	execTool(t, reg, "muninn_remember", callerArgs)

	if _, leaked := callerArgs["vault"]; leaked {
		t.Error("wrapper must not mutate the caller's args map")
	}
}

// TestPinAmbientVaultTools_LeavesNonVaultToolsAlone verifies the prefix filter.
func TestPinAmbientVaultTools_LeavesNonVaultToolsAlone(t *testing.T) {
	reg := tools.NewRegistry()
	other := &recordingVaultTool{name: "read_file"}
	reg.Register(other)

	if n := pinAmbientVaultTools(reg, "vault-a"); n != 0 {
		t.Fatalf("expected 0 tools pinned, got %d", n)
	}
	execTool(t, reg, "read_file", map[string]any{"path": "x"})
	if _, injected := other.lastArgs["vault"]; injected {
		t.Error("non-muninn tools must not be pinned")
	}
}

// TestPinAmbientVaultTools_Idempotent verifies repeated calls (one per agent
// turn) do not stack wrappers.
func TestPinAmbientVaultTools_Idempotent(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&recordingVaultTool{name: "muninn_remember"})

	pinAmbientVaultTools(reg, "vault-a")
	if n := pinAmbientVaultTools(reg, "vault-a"); n != 0 {
		t.Errorf("second pin should be a no-op, pinned %d", n)
	}
}

func TestStripAmbientVaultTools(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&recordingVaultTool{name: "muninn_remember"})
	reg.Register(&recordingVaultTool{name: "muninn_recall"})
	reg.Register(&recordingVaultTool{name: "read_file"})

	if n := stripAmbientVaultTools(reg); n != 2 {
		t.Fatalf("expected 2 tools stripped, got %d", n)
	}
	if _, ok := reg.Get("muninn_remember"); ok {
		t.Error("muninn_remember should have been removed")
	}
	if _, ok := reg.Get("read_file"); !ok {
		t.Error("read_file must survive")
	}
}

// TestResolveAmbientVaultTools_DegradedPathPinsToAgentVault reproduces the
// production failure: connectAgentVault fails ("muninn config unavailable")
// but ambient muninn_* tools from a globally configured MuninnDB MCP server are
// still registered. Those must be pinned to the agent's vault rather than
// silently writing to the MCP server's default vault.
func TestResolveAmbientVaultTools_DegradedPathPinsToAgentVault(t *testing.T) {
	reg := tools.NewRegistry()
	rec := &recordingVaultTool{name: "muninn_remember"}
	reg.Register(rec)
	reg.Register(&recordingVaultTool{name: "muninn_recall"})

	ag := &agents.Agent{Name: "Heimdall", MemoryEnabled: true, VaultName: "huginn-agent-heimdall"}
	vr := vaultResult{sessionReg: reg, cancel: func() {}, warning: "muninn config unavailable"}

	resolveAmbientVaultTools(vr, ag, "workflow-test")

	execTool(t, reg, "muninn_remember", map[string]any{"concept": "Health Check: all clear"})
	if got := rec.lastArgs["vault"]; got != "huginn-agent-heimdall" {
		t.Errorf("degraded path must pin to agent vault, got %v", got)
	}
}

// TestResolveAmbientVaultTools_NoVaultConfiguredStripsTools verifies that an
// agent with memory disabled cannot reach a shared default vault through
// ambient tools.
func TestResolveAmbientVaultTools_NoVaultConfiguredStripsTools(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&recordingVaultTool{name: "muninn_remember"})
	reg.Register(&recordingVaultTool{name: "muninn_recall"})

	ag := &agents.Agent{Name: "Nomem", MemoryEnabled: false}
	vr := vaultResult{sessionReg: reg, cancel: func() {}}

	resolveAmbientVaultTools(vr, ag, "sess-1")

	if _, ok := reg.Get("muninn_remember"); ok {
		t.Error("agent without a vault must not retain ambient muninn tools")
	}
}

// TestResolveAmbientVaultTools_ConnectedPathLeavesToolsUnwrapped verifies the
// healthy path is untouched — connectAgentVault already registered
// vault-scoped tools authenticated by a per-vault token.
func TestResolveAmbientVaultTools_ConnectedPathLeavesToolsUnwrapped(t *testing.T) {
	reg := tools.NewRegistry()
	rec := &recordingVaultTool{name: "muninn_remember"}
	reg.Register(rec)
	reg.Register(&recordingVaultTool{name: "muninn_recall"})

	ag := &agents.Agent{Name: "Odin", MemoryEnabled: true, VaultName: "huginn-fleet"}
	vr := vaultResult{sessionReg: reg, cancel: func() {}} // no warning = connected

	resolveAmbientVaultTools(vr, ag, "sess-2")

	execTool(t, reg, "muninn_remember", map[string]any{"concept": "x"})
	if _, injected := rec.lastArgs["vault"]; injected {
		t.Error("connected path must not wrap tools; the token already scopes the vault")
	}
}
