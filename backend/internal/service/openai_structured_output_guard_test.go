//go:build unit

package service

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func structuredTestResponse(text, status string) string {
	body, _ := json.Marshal(map[string]any{
		"id": "resp_guard", "status": status, "model": "gpt-6-luna",
		"output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": text}}}},
		"usage":  map[string]any{"input_tokens": 1, "output_tokens": 10},
	})
	return string(body)
}

func TestStructuredOutputGuard_RejectsInvalidJSONBeforeReplay(t *testing.T) {
	for _, text := range []string{`{"ok":true}}`, "```json\n{}\n```", `{} {}`, ""} {
		t.Run(text, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":" + structuredTestResponse(text, "completed") + "}\n\n"))}
			err := guardOpenAIStructuredOutput(t.Context(), resp)
			require.ErrorContains(t, err, "invalid structured JSON")
			require.ErrorContains(t, err, "response_id=resp_guard")
			require.ErrorContains(t, err, "sha256=")
		})
	}
}

func TestStructuredOutputGuard_PreservesValidIncompleteAndRefusal(t *testing.T) {
	for _, body := range []string{
		structuredTestResponse(`{"ok":true}`, "completed"),
		structuredTestResponse(`{"ok":`, "incomplete"),
		`{"id":"resp_refusal","status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"cannot comply"}]}]}`,
	} {
		resp := &http.Response{Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
		require.NoError(t, guardOpenAIStructuredOutput(t.Context(), resp))
		replayed, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, body, string(replayed))
	}
}

func TestStructuredOutputGuard_ReconstructsOmittedTerminalOutput(t *testing.T) {
	item := `{"type":"message","content":[{"type":"output_text","text":"{\"ok\":true}"}]}`
	body := "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":" + item + "}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_guard\",\"status\":\"completed\",\"output\":[]}}\n\n"
	resp := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	require.NoError(t, guardOpenAIStructuredOutput(t.Context(), resp))
	replayed, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, body, string(replayed))
}

func TestStructuredOutputGuard_DetectsSSEWithoutContentType(t *testing.T) {
	for _, contentType := range []string{"", "application/json"} {
		body := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + structuredTestResponse(`{"ok":true}`, "completed") + "}\n\n"
		resp := &http.Response{Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
		require.NoError(t, guardOpenAIStructuredOutput(t.Context(), resp))
		replayed, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, body, string(replayed))
	}
}

func TestStructuredOutputGuard_BoundsMemoryAndRejectsMissingTerminal(t *testing.T) {
	for _, body := range []string{strings.Repeat("x", openAIStructuredOutputMaxBytes+1), "data: {\"type\":\"response.created\"}\n\n"} {
		resp := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
		require.Error(t, guardOpenAIStructuredOutput(t.Context(), resp))
	}
}

func TestStructuredOutputGuard_ClosesStalledUpstream(t *testing.T) {
	for _, firstOutput := range []bool{true, false} {
		t.Run(fmt.Sprint(firstOutput), func(t *testing.T) {
			reader, writer := io.Pipe()
			defer writer.Close()
			resp := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}
			deadline := time.Time{}
			idle := 10 * time.Millisecond
			if firstOutput {
				deadline = time.Now().Add(idle)
				idle = 0
			}
			require.Error(t, guardOpenAIStructuredOutputWithTimeouts(t.Context(), resp, deadline, idle))
		})
	}
}

func TestStructuredOutputGuard_ReplayDoesNotRestartExpiredFirstOutputTimer(t *testing.T) {
	body := "data: {\"type\":\"response.completed\",\"response\":" + structuredTestResponse(`{"ok":true}`, "completed") + "}\n\n"
	resp := &http.Response{Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	require.NoError(t, guardOpenAIStructuredOutput(t.Context(), resp))
	svc := newOpenAIImageGenerationControlTestService(&httpUpstreamRecorder{})
	svc.cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 1
	c, recorder := newOpenAIImageGenerationControlTestContext(true, "Go-http-client/2.0")
	_, err := svc.handleStreamingResponse(t.Context(), resp, c, newOpenAIImageGenerationControlTestAccount(), time.Now().Add(-2*time.Second), "gpt-6-luna", "gpt-6-luna")
	require.NoError(t, err)
	require.Contains(t, recorder.Body.String(), "response.completed")
}

func TestStructuredOutputGuard_OnlyToollessStrictJSON(t *testing.T) {
	require.True(t, requiresOpenAIStructuredOutputGuard([]byte(`{"text":{"format":{"type":"json_schema","strict":true}}}`)))
	for _, extra := range []string{`,"tools":[{"type":"function"}]`, `,"previous_response_id":"resp_old"`} {
		require.False(t, requiresOpenAIStructuredOutputGuard([]byte(`{"text":{"format":{"type":"json_schema","strict":true}}`+extra+`}`)))
	}
	require.False(t, requiresOpenAIStructuredOutputGuard([]byte(`{"text":{"format":{"type":"json_schema","strict":false}}}`)))
}

func TestCodexTransform_PreservesNativeOutputCap(t *testing.T) {
	body := map[string]any{"model": "gpt-6-luna", "max_output_tokens": float64(2400), "input": "health check"}
	applyCodexOAuthTransform(body, false, false)
	require.Equal(t, float64(2400), body["max_output_tokens"])
}

func TestStructuredOutputGuard_ForwardRetriesWithoutLeakingInvalidAttempt(t *testing.T) {
	for _, recover := range []bool{true, false} {
		t.Run(fmt.Sprint(recover), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{}
			for attempt := range 2 {
				text := `{"ok":true}}`
				if recover && attempt > 0 {
					text = `{"ok":true}`
				}
				body := "data: {\"type\":\"response.completed\",\"response\":" + structuredTestResponse(text, "completed") + "}\n\n"
				upstream.responses = append(upstream.responses, &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))})
			}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			c, recorder := newOpenAIImageGenerationControlTestContext(true, "Go-http-client/2.0")
			account := newOpenAIImageGenerationControlTestAccount()
			account.Type = AccountTypeOAuth
			account.Credentials = map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "chatgpt-account"}
			_, err := svc.Forward(t.Context(), c, account, []byte(`{"model":"gpt-6-luna","input":"health","stream":true,"max_output_tokens":2400,"text":{"format":{"type":"json_schema","name":"health","strict":true,"schema":{"type":"object"}}}}`))
			require.Len(t, upstream.requests, 2)
			require.EqualValues(t, 2400, gjson.GetBytes(upstream.lastBody, "max_output_tokens").Int())
			if recover {
				require.NoError(t, err)
				require.Contains(t, recorder.Body.String(), "response.completed")
			} else {
				require.Error(t, err)
				require.Empty(t, recorder.Body.String())
			}
			require.NotContains(t, recorder.Body.String(), `true}}`)
		})
	}
}
