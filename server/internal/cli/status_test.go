package cli

import (
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// aboard status lists people, with their access, only once a second person is on the
// board.
func TestStatusListsPeopleOnlyWhenThereAreTwo(t *testing.T) {
	human := func(name string, access api.MemberAccess) api.Member {
		return api.Member{Name: name, Kind: api.MemberKindHuman, Access: &access}
	}
	agent := func(name string) api.Member { return api.Member{Name: name, Kind: api.MemberKindAgent} }
	tests := []struct {
		name    string
		members []api.Member
		want    string
	}{
		{"solo with agents", []api.Member{human("alex", api.MemberAccessAdmin), agent("writer"), agent("reviewer")}, ""},
		{"two people", []api.Member{
			human("alex", api.MemberAccessAdmin), agent("writer"), human("sam", api.MemberAccessMember), agent("reviewer"),
		}, "alex (admin), sam (member)"},
	}
	for _, tt := range tests {
		people := peopleOf(tt.members)
		got := ""
		if people != nil {
			got = peopleText(people)
		}
		if got != tt.want {
			t.Errorf("%s: people %q, want %q", tt.name, got, tt.want)
		}
	}
}
