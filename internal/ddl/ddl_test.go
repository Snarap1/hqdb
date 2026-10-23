package ddl

import "testing"

// fakeConn is a bare adapters.DB stub: GetDDL must reject redis and mongodb
// on the adapter kind alone, before any client call.
type fakeConn struct{ kind string }

func (f fakeConn) Kind() string { return f.kind }
func (f fakeConn) Close()       {}

func TestGetDDLRejectsRedis(t *testing.T) {
	_, err := GetDDL(t.Context(), fakeConn{kind: "redis"}, []string{"plainkey"})
	if err == nil {
		t.Fatal("expected an error")
	}
	want := "get_ddl is not available for redis; use run_redis"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}

func TestGetDDLRejectsMongo(t *testing.T) {
	_, err := GetDDL(t.Context(), fakeConn{kind: "mongodb"}, []string{"hqdb", "widgets"})
	if err == nil {
		t.Fatal("expected an error")
	}
	want := "get_ddl is not available for mongodb; use run_mongo"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
}
