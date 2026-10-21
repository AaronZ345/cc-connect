package slack

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func rawJSON(t *testing.T, s string) *json.RawMessage {
	t.Helper()
	r := json.RawMessage(s)
	return &r
}

// A native Slack table block: header cells as rich_text (bold), body cells as raw_text.
const tableBlock = `{"type":"table","rows":[
 [{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"Item","style":{"bold":true}}]}]},
  {"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"Qty","style":{"bold":true}}]}]}],
 [{"type":"raw_text","text":"apple"},{"type":"raw_text","text":"12"}],
 [{"type":"raw_text","text":"a|b"},{"type":"raw_text","text":"3"}]
]}`

// Shape observed from Slack when a message containing a table is shared into a
// thread: the table lives in attachments[].blocks, the message text has none of it.
const sharedTableAttachment = `{"is_share":true,"author_name":"Alice","fallback":"[shared message]",
 "blocks":[` + tableBlock + `]}`

func TestExtractRichContent_SharedMessageTableInAttachmentBlocks(t *testing.T) {
	ev := `{"text":"<@UBOT> see these","blocks":[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"see these"}]}]}],
	 "attachments":[` + sharedTableAttachment + `]}`
	got := extractRichContent(rawJSON(t, ev), "1.1")
	for _, want := range []string{"[Attachment 1 from Alice]", "| Item | Qty |", "| --- | --- |", "| apple | 12 |", `| a\|b | 3 |`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[shared message]") {
		t.Fatalf("fallback should not be used when blocks render:\n%s", got)
	}
}

func TestExtractRichContent_SharedMessageInMessageBlocks(t *testing.T) {
	ev := `{"text":"x","attachments":[{"is_share":true,"text":"plain fallback",
	 "message_blocks":[{"message":{"blocks":[
	   {"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"original body"}]}]},` + tableBlock + `]}}]}]}`
	got := extractRichContent(rawJSON(t, ev), "1.2")
	if !strings.Contains(got, "original body") || !strings.Contains(got, "| apple | 12 |") {
		t.Fatalf("message_blocks not rendered:\n%s", got)
	}
	if strings.Contains(got, "plain fallback") {
		t.Fatalf("should prefer message_blocks over text:\n%s", got)
	}
}

func TestExtractRichContent_DirectTableBlockSkipsOwnRichText(t *testing.T) {
	ev := `{"text":"look","blocks":[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"look"}]}]},` + tableBlock + `]}`
	got := extractRichContent(rawJSON(t, ev), "1.3")
	if strings.Contains(got, "look") {
		t.Fatalf("the message's own rich_text duplicates text and should be skipped:\n%s", got)
	}
	if !strings.Contains(got, "| apple | 12 |") {
		t.Fatalf("table not rendered:\n%s", got)
	}
}

func TestExtractRichContent_PlainTextOnlyReturnsEmpty(t *testing.T) {
	ev := `{"text":"hi","blocks":[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"hi"}]}]}]}`
	if got := extractRichContent(rawJSON(t, ev), "1.4"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestExtractRichContent_AttachmentTextAndFields(t *testing.T) {
	ev := `{"text":"x","attachments":[{"text":"forwarded body","fields":[{"title":"k","value":"v"}]}]}`
	got := extractRichContent(rawJSON(t, ev), "1.5")
	if !strings.Contains(got, "forwarded body") || !strings.Contains(got, "k: v") {
		t.Fatalf("unexpected:\n%s", got)
	}
}

func TestAppendRichContent(t *testing.T) {
	if appendRichContent("a", "") != "a" || appendRichContent("", "b") != "b" || appendRichContent("a", "b") != "a\n\nb" {
		t.Fatal("appendRichContent mismatch")
	}
}

// callbackEvent wraps an inner event together with its raw JSON, the way
// socketmode delivers it, so handleEvent can read blocks/attachments.
func callbackEvent(inner any, innerType, raw string) socketmode.Event {
	r := json.RawMessage(raw)
	return socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type: slackevents.CallbackEvent,
			Data: &slackevents.EventsAPICallbackEvent{Type: "event_callback", InnerEvent: &r},
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: innerType,
				Data: inner,
			},
		},
	}
}

// Regression: an @-mention carrying a shared message with a native table was
// dispatched with only "see these" — the table in attachments was dropped, so
// the agent replied that no data had been provided.
func TestHandleEvent_MentionWithSharedTableForwardsTable(t *testing.T) {
	p, got := newDedupTestPlatform(t)
	ts := freshTS(1)
	raw := `{"type":"app_mention","user":"U1","channel":"C1","ts":"` + ts + `","text":"<@UBOT> see these",
	 "attachments":[` + sharedTableAttachment + `]}`
	p.handleEvent(callbackEvent(&slackevents.AppMentionEvent{
		User: "U1", Channel: "C1", TimeStamp: ts, Text: "<@UBOT> see these",
	}, "app_mention", raw))

	if len(*got) != 1 {
		t.Fatalf("dispatched %d messages, want 1", len(*got))
	}
	content := (*got)[0].Content
	if !strings.HasPrefix(content, "see these") || !strings.Contains(content, "| apple | 12 |") {
		t.Fatalf("table not forwarded to agent, content:\n%s", content)
	}
}

func TestHandleEvent_MessageWithTableBlockForwardsTable(t *testing.T) {
	p, got := newDedupTestPlatform(t)
	ts := freshTS(2)
	raw := `{"type":"message","user":"U1","channel":"C1","ts":"` + ts + `","text":"look",
	 "blocks":[` + tableBlock + `]}`
	p.handleEvent(callbackEvent(&slackevents.MessageEvent{
		User: "U1", Channel: "C1", TimeStamp: ts, Text: "look",
	}, "message", raw))

	if len(*got) != 1 {
		t.Fatalf("dispatched %d messages, want 1", len(*got))
	}
	if content := (*got)[0].Content; !strings.Contains(content, "| apple | 12 |") {
		t.Fatalf("table not forwarded to agent, content:\n%s", content)
	}
}
