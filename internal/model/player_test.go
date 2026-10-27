package model

import "testing"

func TestValidatePlayer(t *testing.T) {
	validUUID := "069a79f4-44e9-4726-a91b-ef990e6b5624"

	cases := []struct {
		name    string
		player  *Player
		wantErr bool
	}{
		{"valid record", &Player{UUID: validUUID, Username: "Notch"}, false},
		{"nil player", nil, true},
		{"undashed UUID", &Player{UUID: "069a79f444e94726a91bef990e6b5624", Username: "Notch"}, false},
		{"uppercase UUID", &Player{UUID: "069A79F4-44E9-4726-A91B-EF990E6B5624", Username: "Notch"}, false},
		{"empty UUID", &Player{UUID: "", Username: "Notch"}, true},
		{"wrong-length UUID", &Player{UUID: "069a79f4-44e9-4726-a91b-ef990e6b562", Username: "Notch"}, true},
		{"non-hex UUID", &Player{UUID: "zzzz79f4-44e9-4726-a91b-ef990e6b5624", Username: "Notch"}, true},
		{"empty username", &Player{UUID: validUUID, Username: ""}, true},
		{"too-short username", &Player{UUID: validUUID, Username: "ab"}, true},
		{"too-long username", &Player{UUID: validUUID, Username: "abcdefghabcdefghabcdefgh"}, true},
		{"invalid username characters", &Player{UUID: validUUID, Username: "bad name"}, true},
		{"minimum-length username", &Player{UUID: validUUID, Username: "abc"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePlayer(tc.player)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidatePlayer(%+v) error = %v, wantErr = %v", tc.player, err, tc.wantErr)
			}
		})
	}
}
