package temporal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	sdktemporal "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	server "github.com/i2y/ringi-server"
)

// launchInput is what a launching run is started with: all it needs, so that a run going on does
// not depend on the configuration of a later process.
type launchInput struct {
	Key  server.EffectKey `json:"key"`
	Kind string           `json:"kind"`
	// Flow is the name the kind gives the flow; Type and TaskQueue are where it runs.
	Flow      string `json:"flow"`
	Type      string `json:"type"`
	TaskQueue string `json:"task_queue"`
	// Input is the flow's input: the arguments as one object.
	Input json.RawMessage `json:"input"`
	// Outputs are the order of the flow's outputs.
	Outputs []string `json:"outputs,omitempty"`
	// AnswerTimeout is how long one try at telling the server may take (time.Duration's String).
	AnswerTimeout string `json:"answer_timeout"`
}

// endedInput is what a launching run tells the server became of the flow: the input of
// EndedActivity.
type endedInput struct {
	Key     server.EffectKey `json:"key"`
	Failed  bool             `json:"failed,omitempty"`
	Reason  string           `json:"reason,omitempty"`
	Outputs json.RawMessage  `json:"outputs,omitempty"`
}

// launchWorkflow is a launching run: it runs the flow as a child, with the key as its workflow ID,
// and tells the server what became of it, until the server has taken it: done, with the outputs it
// ended with, or failed, with why. Cancelling it cancels the flow. A launching run reset after its
// flow failed runs the flow again under the same ID, and tells the server again.
func launchWorkflow(ctx workflow.Context, in launchInput) error {
	child := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:            in.Key.String(),
		TaskQueue:             in.TaskQueue,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
	})
	var out json.RawMessage
	err := workflow.ExecuteChildWorkflow(child, in.Type, in.Input).Get(ctx, &out)
	e := endedOf(in, out, err)
	if e.Failed {
		workflow.GetLogger(ctx).Warn("the flow failed", "flow", in.Flow, "key", in.Key.String(), "why", e.Reason)
	}
	timeout, perr := time.ParseDuration(in.AnswerTimeout)
	if perr != nil || timeout <= 0 {
		timeout = time.Minute
	}
	retry := answerRetry
	// The server is told even when the run is cancelled: the card shows what became of the work.
	tell, _ := workflow.NewDisconnectedContext(ctx)
	tell = workflow.WithActivityOptions(tell, workflow.ActivityOptions{StartToCloseTimeout: timeout, RetryPolicy: &retry})
	return workflow.ExecuteActivity(tell, EndedActivity, e).Get(tell, nil)
}

// endedOf makes what the server is told from how the flow ended: its outputs out, as named values in
// the order the flow declares them, or its error err, whose innermost message is the reason.
func endedOf(in launchInput, out json.RawMessage, err error) endedInput {
	failed := func(why string) endedInput { return endedInput{Key: in.Key, Failed: true, Reason: fitReason(why)} }
	if err != nil {
		return failed(innermost(err))
	}
	if len(out) == 0 || string(out) == "null" {
		return endedInput{Key: in.Key}
	}
	outputs, err := namedValues(out, in.Outputs)
	if err != nil {
		return failed("the outputs do not fit: " + err.Error())
	}
	return endedInput{Key: in.Key, Outputs: outputs}
}

// innermost answers the message of the innermost error of a chain that is an application's error
// (the error a task or the flow failed with), else the error's own message.
func innermost(err error) string {
	msg := err.Error()
	for e := err; e != nil; e = errors.Unwrap(e) {
		var app *sdktemporal.ApplicationError
		if errors.As(e, &app) {
			msg = app.Message()
			e = app
		}
	}
	return msg
}

// fitReason makes a reason the server takes (server.WorkEnd): a character the server refuses in a
// reason is a space, and a reason longer than server.MaxReason characters is cut, with "…".
func fitReason(s string) string {
	runes := []rune(s)
	if len(runes) > server.MaxReason {
		runes = append(runes[:server.MaxReason-1], '…')
	}
	for i, c := range runes {
		if c != '\n' && server.UnfitChar(c) {
			runes[i] = ' '
		}
	}
	return string(runes)
}

// tell is EndedActivity. NotFound (the server launched nothing by the key) and Invalid fail the
// launching run; an error is tried again.
func (a answers) tell(ctx context.Context, in endedInput) error {
	var outputs []server.NamedValue
	if len(in.Outputs) > 0 {
		if err := json.Unmarshal(in.Outputs, &outputs); err != nil {
			return sdktemporal.NewNonRetryableApplicationError("the outputs of "+in.Key.String()+": "+err.Error(), "requests.outputs", nil)
		}
	}
	r, err := a.to.WorkEnded(ctx, in.Key, server.WorkEnd{Failed: in.Failed, Reason: in.Reason, Outputs: outputs})
	if err != nil {
		return err
	}
	if r.Outcome == server.OK {
		return nil
	}
	return sdktemporal.NewNonRetryableApplicationError(
		fmt.Sprintf("the server answered %s to what became of the work for %s: %s", r.Outcome, in.Key, r.Problem),
		"requests."+string(r.Outcome), nil)
}
