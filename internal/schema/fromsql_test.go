package schema

import "testing"

func TestFromSQL(t *testing.T) {
	s, err := FromSQL(`
		CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT);
		CREATE TABLE posts (id INTEGER PRIMARY KEY, body TEXT);
		CREATE INDEX idx_posts_body ON posts(body);
	`)
	if err != nil {
		t.Fatal(err)
	}
	tm := s.TableMap()
	if len(tm) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(tm))
	}
	users, ok := tm["users"]
	if !ok || len(users.Columns) != 3 {
		t.Errorf("users table not materialized correctly: %+v", users)
	}
	posts := tm["posts"]
	if len(posts.Indexes) != 1 || posts.Indexes[0].Name != "idx_posts_body" {
		t.Errorf("posts index not materialized: %+v", posts.Indexes)
	}
}

func TestFromSQL_Invalid(t *testing.T) {
	if _, err := FromSQL("this is not valid sql;"); err == nil {
		t.Errorf("expected error for invalid schema SQL")
	}
}
