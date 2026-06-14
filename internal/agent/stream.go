package agent

import (
	"bufio"
	"encoding/json"
	"io"
	"sort"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// frameSink is the subset of schema.StreamWriter the SSE parsers need.
type frameSink interface {
	Send(*schema.Message, error) bool
}

// toolAcc accumulates a single tool call's chunks across many SSE frames.
type toolAcc struct {
	id, name string
	args     strings.Builder
}

// reasoningFrame builds a stream frame carrying only a thinking increment.
func reasoningFrame(delta string) *schema.Message {
	m := schema.AssistantMessage("", nil)
	m.ReasoningContent = delta
	return m
}

// flushToolCalls emits accumulated tool-call chunks as one assistant frame,
// ordered by index for determinism. No-op when there are no tool calls.
func flushToolCalls(toolByIndex map[int]*toolAcc, sw frameSink) {
	if len(toolByIndex) == 0 {
		return
	}
	indices := make([]int, 0, len(toolByIndex))
	for i := range toolByIndex {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	msg := schema.AssistantMessage("", nil)
	for _, i := range indices {
		acc := toolByIndex[i]
		if acc.name == "" {
			continue
		}
		idx := i
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			Index: &idx,
			ID:    acc.id,
			Function: schema.FunctionCall{
				Name:      acc.name,
				Arguments: acc.args.String(),
			},
		})
	}
	if len(msg.ToolCalls) > 0 {
		sw.Send(msg, nil)
	}
}

// ---- OpenAI-compatible SSE ----

// streamOpenAI parses a chat-completions SSE stream, emitting reply text and
// reasoning increments as they arrive and accumulating tool-call chunks (keyed
// by index) into a single final frame so the ReAct loop can concat them.
func streamOpenAI(body io.Reader, sw frameSink) error {
	type oaChunk struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				Reasoning        string `json:"reasoning"`
				ToolCalls        []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}

	toolByIndex := map[int]*toolAcc{}
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk oaChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue // skip malformed keep-alive / partial lines
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		if rc := firstNonEmptyStr(d.ReasoningContent, d.Reasoning); rc != "" {
			sw.Send(reasoningFrame(rc), nil)
		}
		if d.Content != "" {
			sw.Send(schema.AssistantMessage(d.Content, nil), nil)
		}
		for _, tc := range d.ToolCalls {
			acc := toolByIndex[tc.Index]
			if acc == nil {
				acc = &toolAcc{}
				toolByIndex[tc.Index] = acc
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}
			acc.args.WriteString(tc.Function.Arguments)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	flushToolCalls(toolByIndex, sw)
	return nil
}

// ---- Anthropic Messages SSE ----

// streamAnthropic parses a Messages API SSE stream, emitting text and thinking
// increments live and accumulating tool_use blocks (by block index) into a
// single final frame.
func streamAnthropic(body io.Reader, sw frameSink) error {
	type aEvent struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
	}

	toolByIndex := map[int]*toolAcc{}
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// Anthropic emits "event: <type>" and "data: <json>" lines; the json
		// carries its own "type", so we only consume the data lines.
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var ev aEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" {
				toolByIndex[ev.Index] = &toolAcc{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					sw.Send(schema.AssistantMessage(ev.Delta.Text, nil), nil)
				}
			case "thinking_delta":
				if ev.Delta.Thinking != "" {
					sw.Send(reasoningFrame(ev.Delta.Thinking), nil)
				}
			case "input_json_delta":
				if acc := toolByIndex[ev.Index]; acc != nil {
					acc.args.WriteString(ev.Delta.PartialJSON)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	flushToolCalls(toolByIndex, sw)
	return nil
}
