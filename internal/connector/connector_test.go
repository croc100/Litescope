package connector

import "testing"

func TestParseTursoDSN(t *testing.T) {
	cases := []struct {
		dsn            string
		wantToken      string
		wantOrg        string
		wantDB         string
		wantErr        bool
	}{
		{"turso://mytoken@myorg/mydb", "mytoken", "myorg", "mydb", false},
		{"turso://tok@org/db-name", "tok", "org", "db-name", false},
		{"turso://missing-at/db", "", "", "", true},
		{"turso://token@org-no-slash", "", "", "", true},
		{"turso://@org/db", "", "", "", true},
	}

	for _, tc := range cases {
		token, org, db, err := parseTursoDSN(tc.dsn)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseTursoDSN(%q): expected error, got nil", tc.dsn)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseTursoDSN(%q): unexpected error: %v", tc.dsn, err)
			continue
		}
		if token != tc.wantToken || org != tc.wantOrg || db != tc.wantDB {
			t.Errorf("parseTursoDSN(%q): got (%s, %s, %s), want (%s, %s, %s)",
				tc.dsn, token, org, db, tc.wantToken, tc.wantOrg, tc.wantDB)
		}
	}
}

func TestParseD1DSN(t *testing.T) {
	cases := []struct {
		dsn            string
		wantToken      string
		wantAccount    string
		wantDB         string
		wantErr        bool
	}{
		{"d1://mytoken@abc123/db-uuid", "mytoken", "abc123", "db-uuid", false},
		{"d1://tok@acc/db", "tok", "acc", "db", false},
		{"d1://missing-at/db", "", "", "", true},
		{"d1://token@acc-no-slash", "", "", "", true},
		{"d1://@acc/db", "", "", "", true},
	}

	for _, tc := range cases {
		token, account, db, err := parseD1DSN(tc.dsn)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseD1DSN(%q): expected error, got nil", tc.dsn)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseD1DSN(%q): unexpected error: %v", tc.dsn, err)
			continue
		}
		if token != tc.wantToken || account != tc.wantAccount || db != tc.wantDB {
			t.Errorf("parseD1DSN(%q): got (%s, %s, %s), want (%s, %s, %s)",
				tc.dsn, token, account, db, tc.wantToken, tc.wantAccount, tc.wantDB)
		}
	}
}

func TestOpenRouting(t *testing.T) {
	cases := []struct {
		dsn     string
		wantErr bool
	}{
		{"tests/fixtures/old.db", false},                        // local file (relative)
		{"turso://bad-token@org/db", false},                     // turso: opens without network call
		{"d1://bad-token@account/db", false},                    // d1: opens without network call
		{"turso://missing@", true},                              // malformed turso
		{"d1://missing@", true},                                 // malformed d1
	}

	for _, tc := range cases {
		_, err := Open(tc.dsn)
		if tc.wantErr && err == nil {
			t.Errorf("Open(%q): expected error, got nil", tc.dsn)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("Open(%q): unexpected error: %v", tc.dsn, err)
		}
	}
}
