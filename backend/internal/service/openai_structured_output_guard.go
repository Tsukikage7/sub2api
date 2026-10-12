package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
)

const openAIStructuredOutputMaxBytes = 8 << 20

type openAIStructuredOutputBody struct{ io.ReadCloser }

// 工具调用和续链保持原有实时流语义；普通严格 JSON 业务请求才暂存校验。
func requiresOpenAIStructuredOutputGuard(body []byte) bool {
	return gjson.GetBytes(body, "text.format.type").String() == "json_schema" &&
		gjson.GetBytes(body, "text.format.strict").Bool() &&
		len(gjson.GetBytes(body, "tools").Array()) == 0 &&
		!gjson.GetBytes(body, "previous_response_id").Exists()
}

// 暂存有明确大小边界的响应。成功后按原始字节重放，拒绝和 incomplete 保留原协议。
func guardOpenAIStructuredOutput(ctx context.Context, resp *http.Response) error {
	return guardOpenAIStructuredOutputWithTimeouts(ctx, resp, time.Time{}, 0)
}

func guardOpenAIStructuredOutputWithTimeouts(ctx context.Context, resp *http.Response, firstOutputDeadline time.Time, idleTimeout time.Duration) error {
	original := resp.Body
	stop := context.AfterFunc(ctx, func() { _ = original.Close() })
	defer stop()
	defer original.Close()
	var firstOutputTimer, idleTimer *time.Timer
	if !firstOutputDeadline.IsZero() {
		firstOutputTimer = time.AfterFunc(time.Until(firstOutputDeadline), func() { _ = original.Close() })
		defer firstOutputTimer.Stop()
	}
	if idleTimeout > 0 {
		idleTimer = time.AfterFunc(idleTimeout, func() { _ = original.Close() })
		defer idleTimer.Stop()
	}
	var buffered bytes.Buffer
	limited := &io.LimitedReader{R: original, N: openAIStructuredOutputMaxBytes + 1}
	reader := bufio.NewReader(limited)
	// OAuth 上游可能缺少 SSE Content-Type，沿用响应处理层的物理行识别规则。
	prefix, _ := reader.Peek(64)
	if isEventStreamResponse(resp.Header) || bodyHasSSEFraming(prefix) {
		accumulator := apicompat.NewBufferedResponseAccumulator()
		doneItems := newResponsesStreamOutputItems()
		terminal := false
		for {
			line, err := reader.ReadString('\n')
			if idleTimer != nil {
				idleTimer.Reset(idleTimeout)
			}
			buffered.WriteString(line)
			if buffered.Len() > openAIStructuredOutputMaxBytes {
				return fmt.Errorf("structured output response exceeds %d bytes", openAIStructuredOutputMaxBytes)
			}
			if data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:"); ok {
				data = strings.TrimSpace(data)
				payload := []byte(data)
				doneItems.Observe(payload)
				var streamEvent apicompat.ResponsesStreamEvent
				if json.Unmarshal(payload, &streamEvent) == nil {
					accumulator.ProcessEvent(&streamEvent)
				}
				event := gjson.Get(data, "type").String()
				if firstOutputTimer != nil && (event == "response.output_text.delta" || strings.HasPrefix(event, "response.reasoning") || event == "response.refusal.delta") {
					firstOutputTimer.Stop()
				}
				switch event {
				case "response.completed", "response.done", "response.incomplete", "response.failed", "response.cancelled", "response.canceled":
					validated, _ := normalizeResponsesStreamingTerminalOutput(payload, accumulator, doneItems, nil)
					if validationErr := validateOpenAIStructuredOutput([]byte(gjson.GetBytes(validated, "response").Raw)); validationErr != nil {
						return validationErr
					}
					// 补齐事件分隔符后即可结束，避免等待保持连接的上游 EOF。
					buffered.WriteByte('\n')
					terminal = true
				}
			}
			if terminal {
				break
			}
			if err != nil {
				return fmt.Errorf("structured output stream ended before terminal response: %w", err)
			}
		}
	} else {
		if _, err := buffered.ReadFrom(reader); err != nil {
			return fmt.Errorf("read structured output: %w", err)
		}
		if buffered.Len() > openAIStructuredOutputMaxBytes {
			return fmt.Errorf("structured output response exceeds %d bytes", openAIStructuredOutputMaxBytes)
		}
		if err := validateOpenAIStructuredOutput(buffered.Bytes()); err != nil {
			return err
		}
	}
	resp.Body = &openAIStructuredOutputBody{io.NopCloser(bytes.NewReader(buffered.Bytes()))}
	resp.ContentLength = int64(buffered.Len())
	resp.Header.Del("Content-Length")
	return nil
}

func validateOpenAIStructuredOutput(body []byte) error {
	var envelope any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("invalid structured response envelope: %w", err)
	}
	response := gjson.ParseBytes(body)
	if response.Get("status").String() != "completed" {
		return nil
	}
	texts := 0
	for _, item := range response.Get("output").Array() {
		for _, part := range item.Get("content").Array() {
			if part.Get("type").String() == "refusal" {
				return nil
			}
			if part.Get("type").String() != "output_text" {
				continue
			}
			text := part.Get("text").String()
			var value any
			if err := json.Unmarshal([]byte(text), &value); err != nil {
				// 留下可关联证据，不把业务原文写入日志。
				return fmt.Errorf("invalid structured JSON: response_id=%s text_bytes=%d sha256=%x: %w", response.Get("id").String(), len(text), sha256.Sum256([]byte(text)), err)
			}
			texts++
		}
	}
	if texts == 0 {
		return fmt.Errorf("completed structured response has no output_text: response_id=%s", response.Get("id").String())
	}
	return nil
}
