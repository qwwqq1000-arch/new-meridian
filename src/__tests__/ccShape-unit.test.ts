import { describe, it, expect } from "bun:test"
import { isClaudeCodeShaped, CC_IDENTITY } from "../proxy/ccShape"

const ccSystem = [{ type: "text", text: `${CC_IDENTITY}\n\nYou are an interactive CLI tool...`, cache_control: { type: "ephemeral" } }]
const ccTools = ["Bash", "Read", "Edit", "Write", "Glob", "Grep", "TodoWrite", "Task"].map(name => ({ name }))

describe("isClaudeCodeShaped", () => {
  it("accepts a genuine Claude Code request (identity + CC tool quorum)", () => {
    expect(isClaudeCodeShaped({ system: ccSystem, tools: ccTools })).toBe(true)
  })

  it("accepts when system is a plain string starting with the identity", () => {
    expect(isClaudeCodeShaped({ system: `${CC_IDENTITY} extra`, tools: ccTools })).toBe(true)
  })

  it("accepts when the CC identity is not the first block (genuine CC prepends a billing-header block)", () => {
    const realCcSystem = [
      { type: "text", text: "x-anthropic-billing-header: cc_version=2.1.148.902; cc_entrypoint=cli" },
      { type: "text", text: `${CC_IDENTITY}\n\nYou are an interactive CLI tool...` },
    ]
    expect(isClaudeCodeShaped({ system: realCcSystem, tools: ccTools })).toBe(true)
  })

  it("rejects an OpenCode-shaped request (lowercase tool names miss the PascalCase quorum)", () => {
    const ocTools = ["read", "write", "edit", "bash", "glob", "grep"].map(name => ({ name }))
    expect(isClaudeCodeShaped({ system: ccSystem, tools: ocTools })).toBe(false)
  })

  it("rejects MCP-prefixed tool names", () => {
    const mcpTools = ["mcp__oc__Read", "mcp__oc__Write", "mcp__oc__Bash", "mcp__oc__Edit"].map(name => ({ name }))
    expect(isClaudeCodeShaped({ system: ccSystem, tools: mcpTools })).toBe(false)
  })

  it("rejects when the CC identity is absent (even with CC tools)", () => {
    expect(isClaudeCodeShaped({ system: [{ type: "text", text: "You are OpenCode." }], tools: ccTools })).toBe(false)
  })

  it("rejects when fewer than the quorum of CC tools is present", () => {
    expect(isClaudeCodeShaped({ system: ccSystem, tools: [{ name: "Bash" }, { name: "Read" }] })).toBe(false)
  })

  it("rejects when there are no tools at all", () => {
    expect(isClaudeCodeShaped({ system: ccSystem })).toBe(false)
    expect(isClaudeCodeShaped({ system: ccSystem, tools: [] })).toBe(false)
  })

  it("rejects an empty / malformed body", () => {
    expect(isClaudeCodeShaped({})).toBe(false)
    expect(isClaudeCodeShaped(null)).toBe(false)
    expect(isClaudeCodeShaped({ system: 123, tools: ccTools })).toBe(false)
  })

  it("honors a custom minTools threshold", () => {
    expect(isClaudeCodeShaped({ system: ccSystem, tools: [{ name: "Bash" }, { name: "Read" }] }, { minTools: 2 })).toBe(true)
  })
})
