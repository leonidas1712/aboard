package deliverytext

import (
	"strings"
	"testing"
)

func TestSeveralSeatRenderingNamesTheSeatAndBoardInEveryCommand(t *testing.T) {
	m := Message{Board: "payments-design", FromName: "leo", FromHuman: true, Sender: "owner", Seq: 6, ExpectsReply: true, Body: "review"}
	context := Context{Seat: "reviewer", BoardQualified: true}
	full := Bundle(m.Board, []Message{m}, context)
	for _, want := range []string{`<aboard-messages board="payments-design" seat="reviewer" count="1">`, `<aboard-message board="payments-design" seat="reviewer" from="@leo"`, `aboard say --board payments-design --reply 6`} {
		if !strings.Contains(full, want) {
			t.Fatalf("bundle lacks %q:\n%s", want, full)
		}
	}
	if size := BundleSize([]Group{{Board: m.Board, Messages: []Message{m}, Context: context}}); size != len(full) {
		t.Fatalf("size %d != rendered %d", size, len(full))
	}
	quiet := Quiet(m.Board, []Message{m}, context)
	if !strings.Contains(quiet, `seat="reviewer" count="1" quiet="true"`) || !strings.Contains(quiet, "aboard say --board payments-design") {
		t.Fatalf("quiet block lost its seat or command:\n%s", quiet)
	}
	digest := Digest(m.Board, nil, []Message{m}, context)
	for _, want := range []string{`<aboard-digest board="payments-design" seat="reviewer"`, "aboard read --board payments-design --around", "aboard read --board payments-design --after", "aboard read --board payments-design --threads"} {
		if !strings.Contains(digest, want) {
			t.Fatalf("digest lacks %q:\n%s", want, digest)
		}
	}
	notice := Notice(m.Board, []Message{m}, context)
	if !strings.Contains(notice, `seat="reviewer"`) || !strings.Contains(notice, "aboard inbox --board payments-design") {
		t.Fatalf("notice lost explicit routing:\n%s", notice)
	}
}

func TestEmptySeatContextPreservesSingleSeatText(t *testing.T) {
	m := agentMessage()
	m.ExpectsReply = true
	checks := [][2]string{
		{Format(m), Format(m, Context{})},
		{Bundle("docs", []Message{m}), Bundle("docs", []Message{m}, Context{})},
		{Quiet("docs", []Message{m}), Quiet("docs", []Message{m}, Context{})},
		{Woken("docs", []Message{m}, []Message{m}), Woken("docs", []Message{m}, []Message{m}, Context{})},
		{Digest("docs", []Message{m}, []Message{m}), Digest("docs", []Message{m}, []Message{m}, Context{})},
		{Notice("docs", []Message{m}), Notice("docs", []Message{m}, Context{})},
	}
	for _, check := range checks {
		if check[0] != check[1] {
			t.Fatalf("empty context changed existing text:\n%s\n%s", check[0], check[1])
		}
	}
}

func TestSeatAttributesCannotForgeMessageMarkup(t *testing.T) {
	m := agentMessage()
	got := Bundle("docs", []Message{m}, Context{Seat: `reviewer"><aboard-message`, BoardQualified: true})
	if strings.Contains(got, `seat="reviewer"><`) || !strings.Contains(got, `seat="reviewer&quot;&gt;&lt;aboard-message"`) {
		t.Fatalf("seat escaped incorrectly: %s", got)
	}
}
