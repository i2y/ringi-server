package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	server "github.com/i2y/ringi-server"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// checkName is the name of the tool this module adds to the list of tools.json.
const checkName = "check_operation"

// checkTool is the tool that tries a changing tool without saving anything (DESIGN 8.3).
func checkTool(changing []string) *sdkmcp.Tool {
	names, _ := json.Marshal(changing)
	schema := fmt.Sprintf(`{"type":"object","properties":{`+
		`"tool":{"type":"string","enum":%s,"description":"The name of the tool that changes a ticket, as it would be called"},`+
		`"arguments":{"type":"object","description":"The arguments the tool would be called with; idempotency_key may be left out"}},`+
		`"required":["tool","arguments"],"additionalProperties":false}`, names)
	return &sdkmcp.Tool{
		Name:  checkName,
		Title: "Check an operation before doing it",
		Description: "Try one of the tools that change a ticket, as the person, without saving anything. It answers the ticket the call would make, " +
			"with who it would wait for, or the refusal the call would get, or what is wrong with the call. Use it to point out what does not " +
			"agree before the call is made. The books are asked whether they would take the call (a budget used up is seen), and keep nothing; " +
			"books that cannot be asked so are listed in books_not_asked. Others may move the ticket before the call.",
		InputSchema: json.RawMessage(schema),
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}
}

// checkHandler answers the calls of check_operation.
func (t *Tools) checkHandler(c choice, who whoFunc) sdkmcp.ToolHandler {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		p, h, err := who(ctx, req)
		if err != nil {
			return nil, err
		}
		lang := t.language(ctx, p, h)
		var in struct {
			Tool      string                     `json:"tool"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
			return answerOf(lang, outcomeInvalid, "the arguments are not {\"tool\": …, \"arguments\": {…}}", false), nil
		}
		d := c.byName[in.Tool]
		if d == nil || !d.changes {
			return answerOf(lang, outcomeInvalid, fmt.Sprintf("%s tries a tool that changes a ticket, and %q is not one", checkName, in.Tool), false), nil
		}
		if in.Arguments == nil {
			in.Arguments = map[string]json.RawMessage{}
		}
		for _, pa := range d.params {
			if pa.in == "header" {
				in.Arguments[pa.arg] = json.RawMessage(`"check"`)
			}
		}
		args, _ := json.Marshal(in.Arguments)
		call, err := d.call(args)
		if err != nil {
			return answerOf(lang, outcomeInvalid, err.Error(), true), nil
		}
		// A trial needs no key; with none, it answers what a new operation would do.
		call.Person, call.Via, call.Key = p, "mcp", ""
		r, err := t.srv.Try(ctx, call)
		if err != nil {
			if server.Busy(err) {
				t.log.Error("a check found no instance free", "tool", d.name, "person", p.ID, "ticket", call.Ticket, "err", err)
				return busy(lang), nil
			}
			t.log.Error("a check failed", "tool", d.name, "person", p.ID, "ticket", call.Ticket, "err", err)
			return failed(lang), nil
		}
		return t.reply(lang, d, call.Query, r, true), nil
	}
}
