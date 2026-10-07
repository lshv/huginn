package agent

import (
	"context"
	"log/slog"
	"strings"

	"github.com/scrypster/huginn/internal/agents"
	"github.com/scrypster/huginn/internal/tools"
)

// vaultToolPrefix identifies MuninnDB tools by name. Both the per-agent vault
// connection (connectAgentVault) and any globally configured MuninnDB MCP
// server register their tools under this prefix.
const vaultToolPrefix = "muninn_"

// vaultScopedTool wraps an ambient MuninnDB tool — one registered by the global
// MCP manager rather than by connectAgentVault — so that calls omitting a
// "vault" argument are pinned to the agent's own vault.
//
// Without this, an unscoped muninn_remember lands in whatever default vault the
// MCP server was configured with (e.g. MUNINN_DEFAULT_VAULT), which silently
// merges every agent's memory into one shared vault.
type vaultScopedTool struct {
	tools.Tool
	vault string
}

// Execute injects the pinned vault when the caller did not specify one.
// The caller's args map is never mutated.
func (t *vaultScopedTool) Execute(ctx context.Context, args map[string]any) tools.ToolResult {
	if v, _ := args["vault"].(string); strings.TrimSpace(v) != "" {
		return t.Tool.Execute(ctx, args)
	}
	scoped := make(map[string]any, len(args)+1)
	for k, v := range args {
		scoped[k] = v
	}
	scoped["vault"] = t.vault
	return t.Tool.Execute(ctx, scoped)
}

// pinAmbientVaultTools rewrites every muninn_* tool in reg so that calls which
// omit a "vault" argument are routed to vaultName. Returns the number of tools
// pinned.
//
// Used on the degraded path: connectAgentVault failed, but ambient MuninnDB
// tools are still registered and would otherwise write to the MCP server's
// default vault instead of this agent's.
func pinAmbientVaultTools(reg *tools.Registry, vaultName string) int {
	if reg == nil || strings.TrimSpace(vaultName) == "" {
		return 0
	}
	pinned := 0
	for _, t := range reg.All() {
		name := t.Name()
		if !strings.HasPrefix(name, vaultToolPrefix) {
			continue
		}
		if _, already := t.(*vaultScopedTool); already {
			continue
		}
		// Unregister first so Register does not log an overwrite warning on
		// every agent turn.
		reg.Unregister(name)
		reg.Register(&vaultScopedTool{Tool: t, vault: vaultName})
		pinned++
	}
	return pinned
}

// resolveAmbientVaultTools reconciles the muninn_* tools visible to this agent
// with the vault it is actually entitled to, and logs what memory the agent
// really has.
//
// Three cases:
//   - vault connected: connectAgentVault registered vault-scoped tools into the
//     fork, overriding any ambient ones. Nothing to do.
//   - agent has no vault: strip ambient tools so memory is genuinely off.
//   - vault connect failed but ambient tools exist: pin them to the agent's
//     vault so the degraded path writes to the right place.
func resolveAmbientVaultTools(vr vaultResult, ag *agents.Agent, sessionID string) {
	if _, ok := vr.sessionReg.Get("muninn_recall"); !ok {
		return
	}
	agentName := ""
	if ag != nil {
		agentName = ag.Name
	}
	switch {
	case ag == nil || !ag.MemoryEnabled || ag.VaultName == "":
		if n := stripAmbientVaultTools(vr.sessionReg); n > 0 {
			slog.Warn("vault: no vault configured for agent; removed ambient MuninnDB tools",
				"agent", agentName, "session_id", sessionID, "tools_removed", n)
		}
	case vr.warning != "":
		n := pinAmbientVaultTools(vr.sessionReg, ag.VaultName)
		slog.Warn("vault: per-agent connection failed; using ambient MuninnDB tools pinned to agent vault",
			"agent", agentName, "session_id", sessionID, "vault", ag.VaultName,
			"warning", vr.warning, "tools_pinned", n)
	default:
		slog.Info("vault tools available",
			"agent", agentName, "session_id", sessionID, "vault", ag.VaultName)
	}
}

// stripAmbientVaultTools removes every muninn_* tool from reg. Returns the
// number of tools removed.
//
// Used when an agent has no vault configured at all: ambient MuninnDB tools
// would otherwise let it read and write a shared default vault despite memory
// being switched off for that agent.
func stripAmbientVaultTools(reg *tools.Registry) int {
	if reg == nil {
		return 0
	}
	stripped := 0
	for _, t := range reg.All() {
		if !strings.HasPrefix(t.Name(), vaultToolPrefix) {
			continue
		}
		reg.Unregister(t.Name())
		stripped++
	}
	return stripped
}
