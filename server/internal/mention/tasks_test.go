package mention

import (
	"reflect"
	"testing"
)

func TestTasksFollowTheTextBoundariesOfMentions(t *testing.T) {
	for _, tc := range []struct {
		body string
		want []string
	}{
		{"CHK-12 and chk-2, again CHK-12", []string{"CHK-12", "CHK-2", "CHK-12"}},
		{"`CHK-1` https://example.com/CHK-2 CHK-3", []string{"CHK-3"}},
		{"~~~go\nCHK-1\n~~~\nCHK-2", []string{"CHK-2"}},
		{"longprefixCHK-1 CHK-1x CHK-0 CHK-01 \\CHK-2 path/CHK-3 CHK-4", []string{"CHK-4"}},
	} {
		if got := Tasks(tc.body); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Tasks(%q)=%v, want %v", tc.body, got, tc.want)
		}
	}
}
