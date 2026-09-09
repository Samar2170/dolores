// Package agent implements a generic analysis agent: it takes a prompt plus
// optional inline data, can consult a company's archived documents in Archivus
// (parsed PDF text included), and falls back to browsing the public web when
// the local evidence is not enough for a satisfactory analysis.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"dolores/internal/llm"
)

const (
	finalTool   = "final"
	MaxRequests = 24
	MaxTokens   = 250000
)

// Tool is one capability the agent can invoke. Unlike tool.Tool, Run returns
// the output as a string so the agent loop can feed it back to the model.
type Tool interface {
	Name() string
	Description() string
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Request is the agent's input: the task prompt, optional inline data and an
// optional company symbol that scopes archive access.
type Request struct {
	Prompt string
	Data   string
	Symbol string
}

// Result carries the final answer plus run diagnostics.
type Result struct {
	Answer       string
	Satisfactory bool
	Sources      []string
	ToolsUsed    []string
	Steps        int
	WebUsed      bool
}

type Agent struct {
	lg        *llm.Client
	tools     map[string]Tool
	order     []string
	maxSteps  int
	webLimit  int
	resultCap int
	actionCap int
}

// New creates an agent over the given LLM client with the listed tools.
func New(lg *llm.Client, tools ...Tool) *Agent {
	a := &Agent{
		lg:        lg,
		tools:     make(map[string]Tool),
		maxSteps:  10,
		webLimit:  8,
		resultCap: 12000,
		actionCap: 1200,
	}
	for _, t := range tools {
		a.Register(t)
	}
	return a
}

// Register adds a tool; a duplicate name panics (wiring is static).
func (a *Agent) Register(t Tool) {
	if _, dup := a.tools[t.Name()]; dup {
		panic(fmt.Sprintf("agent: duplicate tool: %s", t.Name()))
	}
	a.tools[t.Name()] = t
	a.order = append(a.order, t.Name())
}

// WithMaxSteps caps the number of agent turns (default 10).
func (a *Agent) WithMaxSteps(n int) *Agent {
	if n > 0 {
		a.maxSteps = n
	}
	return a
}

// WithWebLimit caps how many web tool calls the agent may make.
func (a *Agent) WithWebLimit(n int) *Agent {
	if n >= 0 {
		a.webLimit = n
	}
	return a
}

type reply struct {
	Thought      string          `json:"thought"`
	Tool         string          `json:"tool"`
	Args         json.RawMessage `json:"args"`
	Answer       string          `json:"answer"`
	Satisfactory *bool           `json:"satisfactory"`
	Sources      []string        `json:"sources"`
}

const webPushNote = `You assessed the analysis as unsatisfactory but tried to finish. Gather the missing evidence now: call web_search with precise queries for the gaps and web_fetch on the promising results (PDFs are parsed automatically). Once the web closes the gap - or clearly cannot - finish with the revised analysis, set satisfactory=true and list the remaining gaps explicitly in the answer.`

const wrapUpNote = `STEP BUDGET WARNING: only one step is left. Finish NOW with the best answer the gathered evidence supports; state any remaining gaps explicitly in the answer instead of calling more tools.`

// Run executes the agent loop: each turn the model either calls one tool or
// finishes. A final answer flagged unsatisfactory triggers a forced web
// research round (up to two) before the loop accepts it.
func (a *Agent) Run(ctx context.Context, req Request) (*Result, error) {
	res := &Result{Satisfactory: true}
	seen := map[string]bool{}
	webCalls, forced, warned := 0, 0, false
	transcript := a.opening(req)

	for step := 1; step <= a.maxSteps; step++ {
		if llm.Exhausted(ctx) {
			return nil, fmt.Errorf("agent: llm budget exhausted before a final answer (step %d)", step)
		}
		if !warned && step == a.maxSteps {
			warned = true
			transcript += a.noteBlock(wrapUpNote)
		}
		raw, err := a.lg.CompleteJSON(ctx, a.systemPrompt(), transcript)
		if err != nil {
			return nil, fmt.Errorf("agent: step %d: %w", step, err)
		}
		var r reply
		if perr := json.Unmarshal(raw, &r); perr != nil {
			log.Printf("[agent] step %d: invalid reply, retrying: %v", step, perr)
			transcript += a.noteBlock(fmt.Sprintf("Reply rejected: %v. Reply again with exactly one JSON object: a tool call or the final answer.", perr))
			continue
		}
		name := strings.ToLower(strings.TrimSpace(r.Tool))
		if name == "" && strings.TrimSpace(r.Answer) != "" {
			name = finalTool
		}
		if name == finalTool {
			log.Printf("[agent] step %d: final answer (%d chars, satisfactory=%t)", step, len(r.Answer), r.Satisfactory == nil || *r.Satisfactory)
			res.Answer = strings.TrimSpace(r.Answer)
			res.Satisfactory = r.Satisfactory == nil || *r.Satisfactory
			res.Sources = r.Sources
			res.Steps = step
			if !res.Satisfactory && a.hasWeb() && webCalls < a.webLimit && forced < 2 {
				forced++
				transcript += a.actionBlock(step, raw) + a.noteBlock(webPushNote)
				continue
			}
			return res, nil
		}
		t, ok := a.tools[name]
		if !ok {
			log.Printf("[agent] step %d: unknown tool %q", step, name)
			transcript += a.actionBlock(step, raw) + a.noteBlock(fmt.Sprintf("Unknown tool %q. Available: %s, %s.", name, strings.Join(a.order, ", "), finalTool))
			continue
		}
		if strings.HasPrefix(name, "web_") {
			webCalls++
			res.WebUsed = true
		}
		if !seen[name] {
			seen[name] = true
			res.ToolsUsed = append(res.ToolsUsed, name)
		}
		args := r.Args
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		started := time.Now()
		out, terr := t.Run(ctx, args)
		if terr != nil {
			log.Printf("[agent] step %d: tool %s FAILED in %s: %v", step, name, time.Since(started).Round(time.Millisecond), terr)
			out = "ERROR: " + terr.Error()
		} else {
			log.Printf("[agent] step %d: tool %s ok in %s (%d chars)", step, name, time.Since(started).Round(time.Millisecond), len(out))
		}
		transcript += a.actionBlock(step, raw) + a.resultBlock(name, clipText(out, a.resultCap))
	}
	return nil, fmt.Errorf("agent: no final answer after %d steps", a.maxSteps)
}

const systemIntro = `You are a rigorous analysis agent. You complete the analysis task given by the user, working from the provided data, the company document archive and, only when necessary, the public web.

Reply with exactly ONE JSON object per turn, no markdown fences, no extra text, in one of these shapes:
{"thought":"why this step","tool":"<tool_name>","args":{...}}
{"thought":"why done","tool":"final","answer":"<the complete analysis>","satisfactory":true,"sources":["..."]}

Rules:
- Read the provided data first; consult the company archive with archivus_list and archivus_read when more evidence is needed. Parsed text of archived PDFs is retrieved automatically.
- Use web_search and web_fetch only when the data and the archive cannot produce a satisfactory analysis, or to verify a critical claim.
- If you believe the analysis is not yet satisfactory, do NOT finish: keep calling tools (prefer web_search, then web_fetch) until the gap is closed, then finish.
- When nothing more can be found, finish with satisfactory=true and state the remaining gaps explicitly in the answer.
- Never invent facts or numbers. Cite sources (archive path or URL) for key claims in the sources array.
- One tool call per reply.`

func (a *Agent) systemPrompt() string {
	var sb strings.Builder
	sb.WriteString(systemIntro)
	sb.WriteString("\n\nTools:\n")
	for _, n := range a.order {
		fmt.Fprintf(&sb, "- %s: %s\n", n, a.tools[n].Description())
	}
	if !a.hasWeb() {
		sb.WriteString("\nWeb tools are disabled in this session; work only from the provided data and the archive.\n")
	}
	return sb.String()
}

func (a *Agent) hasWeb() bool {
	for _, n := range a.order {
		if strings.HasPrefix(n, "web_") {
			return true
		}
	}
	return false
}

func (a *Agent) opening(req Request) string {
	var sb strings.Builder
	sb.WriteString("TASK:\n")
	sb.WriteString(strings.TrimSpace(req.Prompt))
	sb.WriteString("\n\nPROVIDED DATA:\n")
	data := strings.TrimSpace(req.Data)
	if data == "" {
		sb.WriteString("(none)\n")
	} else {
		sb.WriteString(clipText(data, 120000))
		sb.WriteString("\n")
	}
	if sym := strings.TrimSpace(req.Symbol); sym != "" {
		fmt.Fprintf(&sb, "\nCOMPANY ARCHIVE: the Archivus files of company %q are reachable through archivus_list and archivus_read (parsed PDF text is picked up automatically).\n", sym)
	}
	return sb.String()
}

func (a *Agent) actionBlock(step int, raw json.RawMessage) string {
	return fmt.Sprintf("\n--- step %d agent action ---\n%s\n", step, clipText(string(raw), a.actionCap))
}

func (a *Agent) resultBlock(name, out string) string {
	return fmt.Sprintf("--- tool %s result ---\n%s\n", name, out)
}

func (a *Agent) noteBlock(msg string) string {
	return fmt.Sprintf("--- supervisor note ---\n%s\n", msg)
}

func clipText(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "\n...[truncated]"
}
