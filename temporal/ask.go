package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	sdktemporal "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	server "github.com/i2y/ringi-server"
)

// askInput is what an asking run is started with: all it needs, so that a run going on does not
// depend on the configuration of a later process.
type askInput struct {
	Key   server.EffectKey `json:"key"`
	Kind  string           `json:"kind"`
	Stage string           `json:"stage"`
	// Note says it is a note (ask_note); else it is an agent's step (ask_agent).
	Note bool `json:"note,omitempty"`
	// Flow is the name the kind gives the flow; Type and TaskQueue are where it runs.
	Flow      string `json:"flow"`
	Type      string `json:"type"`
	TaskQueue string `json:"task_queue"`
	// Input is the flow's input: the arguments as one object.
	Input json.RawMessage `json:"input"`
	// Outputs are the order of a note's outputs.
	Outputs []string `json:"outputs,omitempty"`
	// AnswerTimeout is how long one try at giving the answer may take (time.Duration's String).
	AnswerTimeout string `json:"answer_timeout"`
}

// AskResult is what an asking run ends with, once the server has taken its answer.
type AskResult struct {
	// Outcome is "moved" (the ticket took the answer) or "refused" (the ticket had gone on).
	Outcome string `json:"outcome"`
	// Refused is the kind of the refusal (stale, state), for "refused".
	Refused string `json:"refused,omitempty"`
}

// answerInput is the answer an asking run gives the server: the input of AnswerActivity.
type answerInput struct {
	Key     server.EffectKey `json:"key"`
	Verdict string           `json:"verdict,omitempty"`
	Reason  string           `json:"reason,omitempty"`
	Outputs json.RawMessage  `json:"outputs,omitempty"`
	Failed  bool             `json:"failed,omitempty"`
	// Why says why the flow gave no answer, for Failed: for whoever reads the run, not the server.
	Why string `json:"why,omitempty"`
}

// answerRetry is how a failed try at giving an answer is tried again: after 2 seconds, then
// doubling up to 10 minutes between tries, with no end.
var answerRetry = sdktemporal.RetryPolicy{
	InitialInterval:    2 * time.Second,
	BackoffCoefficient: 2,
	MaximumInterval:    10 * time.Minute,
}

// askWorkflow is an asking run: it runs the flow as a child, with the key as its workflow ID,
// makes the answer from how it ended, and gives it to the server until the server has taken it.
// Cancelling it cancels the flow; once the flow has ended, the server is given Failed (or the
// flow's answer, when it ended with one).
func askWorkflow(ctx workflow.Context, in askInput) (AskResult, error) {
	child := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:            in.Key.String(),
		TaskQueue:             in.TaskQueue,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	})
	var out json.RawMessage
	err := workflow.ExecuteChildWorkflow(child, in.Type, in.Input).Get(ctx, &out)
	a := answerOf(in, out, err)
	if a.Failed {
		workflow.GetLogger(ctx).Warn("the flow gave no answer", "flow", in.Flow, "key", in.Key.String(), "why", a.Why)
	}
	timeout, perr := time.ParseDuration(in.AnswerTimeout)
	if perr != nil || timeout <= 0 {
		timeout = time.Minute
	}
	retry := answerRetry
	// The answer is given even when the run is cancelled: the server waits for it.
	give, _ := workflow.NewDisconnectedContext(ctx)
	give = workflow.WithActivityOptions(give, workflow.ActivityOptions{StartToCloseTimeout: timeout, RetryPolicy: &retry})
	var res AskResult
	err = workflow.ExecuteActivity(give, AnswerActivity, a).Get(give, &res)
	return res, err
}

// answerOf makes the answer from how the flow ended: its outputs out, or its error err.
func answerOf(in askInput, out json.RawMessage, err error) answerInput {
	failed := func(why string) answerInput { return answerInput{Key: in.Key, Failed: true, Why: why} }
	if err != nil {
		return failed(err.Error())
	}
	if in.Note {
		outputs, err := namedValues(out, in.Outputs)
		if err != nil {
			return failed("the outputs do not fit: " + err.Error())
		}
		return answerInput{Key: in.Key, Outputs: outputs}
	}
	fields, err := readObject(out)
	if err != nil {
		return failed("the outputs do not fit: " + err.Error())
	}
	var verdict, reason *string
	for _, f := range fields {
		switch f.name {
		case "verdict":
			verdict = text(f.value)
		case "reason":
			if string(f.value) != "null" {
				if reason = text(f.value); reason == nil {
					return failed("the reason is not a text: " + string(f.value))
				}
			}
		}
	}
	switch {
	case verdict == nil:
		return failed("the outputs have no verdict")
	case *verdict != "ok" && *verdict != "doubt":
		return failed(fmt.Sprintf("the verdict %q is neither ok nor doubt", *verdict))
	}
	a := answerInput{Key: in.Key, Verdict: *verdict}
	if reason != nil {
		a.Reason = *reason
	}
	return a
}

// text answers a JSON string's text, or nil for any other value.
func text(v json.RawMessage) *string {
	var s string
	if len(v) == 0 || v[0] != '"' || json.Unmarshal(v, &s) != nil {
		return nil
	}
	return &s
}

// answers gives the asking runs' answers to the server.
type answers struct{ to Answerer }

// give is AnswerActivity. A refusal is an answer: the ticket went on since it asked. NotFound (the
// server asked nothing by the key) and Invalid fail the asking run; an error is tried again.
func (a answers) give(ctx context.Context, in answerInput) (AskResult, error) {
	r, err := a.to.Answered(ctx, in.Key, server.Answer{Verdict: in.Verdict, Reason: in.Reason, Outputs: in.Outputs, Failed: in.Failed})
	if err != nil {
		return AskResult{}, err
	}
	switch r.Outcome {
	case server.OK:
		return AskResult{Outcome: "moved"}, nil
	case server.Refused:
		var b struct {
			Refused string `json:"refused"`
		}
		_ = json.Unmarshal(r.Body, &b)
		return AskResult{Outcome: "refused", Refused: b.Refused}, nil
	}
	return AskResult{}, sdktemporal.NewNonRetryableApplicationError(
		fmt.Sprintf("the server answered %s to the answer for %s: %s", r.Outcome, in.Key, r.Problem),
		"requests."+string(r.Outcome), nil)
}
