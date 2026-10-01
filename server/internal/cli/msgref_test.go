package cli

import "testing"

func TestParseMessageRef(t *testing.T) {
	tests := []struct {
		in      string
		want    messageRef
		wantErr bool
	}{
		{"msg_01M3VD4G47TACAPA2J7JKG1JF1", messageRef{ID: "msg_01M3VD4G47TACAPA2J7JKG1JF1"}, false},
		{"6", messageRef{Seq: 6}, false},
		{"#6", messageRef{Seq: 6}, false},
		{" #12 ", messageRef{Seq: 12}, false},
		{"writer-reviewer#6", messageRef{Board: "writer-reviewer", Seq: 6}, false},
		{"msg_", messageRef{}, true},
		{"0", messageRef{}, true},
		{"-3", messageRef{}, true},
		{"+3", messageRef{}, true},
		{"#", messageRef{}, true},
		{"six", messageRef{}, true},
		{"Board#6", messageRef{}, true},
		{"a#6", messageRef{}, true},
		{"docs#six", messageRef{}, true},
		{"docs#6#7", messageRef{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseMessageRef(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if e := asError(err); e.Code != "message_ref_invalid" {
					t.Errorf("code = %q, want message_ref_invalid", e.Code)
				}
				return
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
