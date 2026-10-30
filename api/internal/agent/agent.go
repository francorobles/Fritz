// Package agent is Fritz's tool-calling loop: Muse Spark answers
// the user, and when it needs facts it can't know (the inbox, the
// calendar, the camera) it calls a tool, Go runs it, and Spark
// answers again with the result. See the fritz-agent skill.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/francorobles/fritz/api/internal/meta"
)

// Tool is one function Spark may call. Run gets the raw JSON
// arguments (model output: validate them) and returns a value that
// is sent back as JSON. Read-only tools only for now: anything that
// sends or creates must go through the confirmation rule first.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema object
	Run         func(ctx context.Context, args json.RawMessage) (any, error)
}

// Streamer is the slice of meta.Client the loop needs (a fake in
// tests).
type Streamer interface {
	ChatStreamTools(ctx context.Context, messages []meta.Message, tools []meta.Tool, emit func(delta string)) (meta.StreamResult, error)
}

// DefaultMaxRounds caps tool rounds per turn (fritz-agent skill):
// enough for search → read, small enough that a confused model
// can't keep the user waiting.
const DefaultMaxRounds = 6

// Agent runs one turn at a time; it holds no conversation state.
type Agent struct {
	Chat      Streamer
	Tools     []Tool
	MaxRounds int // 0 means DefaultMaxRounds

	// OnTool, when set, is told about every call before it runs
	// (console log, face "thinking").
	OnTool func(name, args string)
}

// Run answers one turn. messages is the full prompt (system,
// history, user). Text is emitted as it streams, across rounds, so
// "Let me check." is spoken while the tool runs. Returns all the
// text that was emitted; on error, what arrived before it.
func (a *Agent) Run(ctx context.Context, messages []meta.Message, emit func(delta string)) (string, error) {
	maxRounds := a.MaxRounds
	if maxRounds <= 0 {
		maxRounds = DefaultMaxRounds
	}
	defs := make([]meta.Tool, len(a.Tools))
	for i, t := range a.Tools {
		defs[i] = meta.Tool{Name: t.Name, Description: t.Description, Parameters: t.Parameters}
	}
	msgs := append([]meta.Message(nil), messages...)

	var text strings.Builder
	for round := 0; ; round++ {
		// Rounds stream into one reply: separate them, or "check."
		// and "You have" run together and the sentence splitter
		// never sees the break.
		roundEmit := emit
		if text.Len() > 0 && !strings.HasSuffix(text.String(), " ") {
			first := true
			roundEmit = func(delta string) {
				if first {
					first = false
					text.WriteString(" ")
					emit(" ")
				}
				emit(delta)
			}
		}
		res, err := a.Chat.ChatStreamTools(ctx, msgs, defs, roundEmit)
		text.WriteString(res.Text)
		if err != nil {
			return text.String(), err
		}
		if len(res.ToolCalls) == 0 {
			return text.String(), nil
		}
		if round >= maxRounds {
			return text.String(), fmt.Errorf("agent: still calling tools after %d rounds", maxRounds)
		}
		msgs = append(msgs, meta.Message{Role: "assistant", Content: res.Text, ToolCalls: res.ToolCalls})
		for _, call := range res.ToolCalls {
			result := a.call(ctx, call)
			if round == maxRounds-1 {
				// Last allowed round: the next reply must be the
				// answer. The tools stay listed (the history
				// refers to them), so ask in words instead.
				result = `{"note":"tool limit reached — answer the user now with what you have","result":` + result + `}`
			}
			msgs = append(msgs, meta.Message{Role: "tool", ToolCallID: call.ID, Content: result})
		}
	}
}

// call runs one tool call and returns its JSON result. Failures go
// back to the model as {"error": ...} so it can tell the user,
// instead of failing the whole turn.
func (a *Agent) call(ctx context.Context, call meta.ToolCall) string {
	if a.OnTool != nil {
		a.OnTool(call.Function.Name, call.Function.Arguments)
	}
	var tool *Tool
	for i := range a.Tools {
		if a.Tools[i].Name == call.Function.Name {
			tool = &a.Tools[i]
			break
		}
	}
	if tool == nil {
		return errJSON(fmt.Errorf("no tool named %q", call.Function.Name))
	}
	args := json.RawMessage(call.Function.Arguments)
	if strings.TrimSpace(call.Function.Arguments) == "" {
		args = json.RawMessage("{}")
	}
	if !json.Valid(args) {
		return errJSON(fmt.Errorf("arguments are not valid JSON"))
	}
	out, err := tool.Run(ctx, args)
	if err != nil {
		return errJSON(err)
	}
	data, err := json.Marshal(out)
	if err != nil {
		return errJSON(err)
	}
	return string(data)
}

func errJSON(err error) string {
	data, _ := json.Marshal(map[string]string{"error": err.Error()})
	return string(data)
}

// decodeArgs parses tool arguments strictly: unknown fields mean
// the model invented a parameter, which is worth telling it.
func decodeArgs(raw json.RawMessage, into any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("bad arguments: %w", err)
	}
	return nil
}
