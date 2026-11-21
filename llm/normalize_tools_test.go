package llm

import (
	"reflect"
	"testing"
)

func use(ids ...string) Message {
	m := Message{Role: RoleAssistant}
	for _, id := range ids {
		m.Content = append(m.Content, ContentBlock{Type: BlockToolUse, ID: id, Name: "Read", Input: []byte(`{}`)})
	}
	return m
}

func res(ids ...string) Message {
	m := Message{Role: RoleUser}
	for _, id := range ids {
		m.Content = append(m.Content, ToolResultText(id, "out "+id, false))
	}
	return m
}

func say(role Role, s string) Message {
	return Message{Role: role, Content: []ContentBlock{TextBlock(s)}}
}

// shape returns a compact description: U<ids> for tool_use, R<ids> for results, text otherwise.
func shape(msgs []Message) []string {
	var out []string
	for _, m := range msgs {
		s := ""
		for _, b := range m.Content {
			switch b.Type {
			case BlockToolUse:
				s += "U" + b.ID
			case BlockToolResult:
				s += "R" + b.ToolUseID
			default:
				s += b.Text
			}
		}
		out = append(out, s)
	}
	return out
}

func TestNormalizeToolExchanges(t *testing.T) {
	cases := []struct {
		name string
		in   []Message
		want []string
	}{
		{"well formed is unchanged",
			[]Message{say(RoleUser, "q"), use("a"), res("a"), say(RoleAssistant, "ok")},
			[]string{"q", "Ua", "Ra", "ok"}},
		{"parallel calls, one message of results",
			[]Message{use("a", "b"), res("a", "b")},
			[]string{"UaUb", "RaRb"}},
		{"result before its tool_use is dropped with the call",
			[]Message{res("a"), say(RoleAssistant, "x"), use("a")},
			[]string{"x"}},
		{"message between tool_use and tool_result moves after the exchange",
			[]Message{use("a"), say(RoleUser, "summary"), res("a")},
			[]string{"Ua", "Ra", "summary"}},
		{"duplicate tool_use and tool_result keep the first",
			[]Message{use("a"), res("a"), use("a"), res("a")},
			[]string{"Ua", "Ra"}},
		{"orphan result is dropped",
			[]Message{say(RoleUser, "q"), res("zz")},
			[]string{"q"}},
		{"parallel pair split by a summary",
			[]Message{use("a", "b"), say(RoleUser, "S"), res("a"), res("b")},
			[]string{"UaUb", "Ra", "Rb", "S"}},
	}
	for _, c := range cases {
		got := shape(NormalizeToolExchanges(c.in))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
		// Idempotent: a normalized history is a fixed point.
		again := shape(NormalizeToolExchanges(NormalizeToolExchanges(c.in)))
		if !reflect.DeepEqual(again, got) {
			t.Errorf("%s: not idempotent: %v then %v", c.name, got, again)
		}
	}
}
