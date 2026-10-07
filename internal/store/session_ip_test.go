package store

import (
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"testing"
	"time"
)

func TestMemorySessionIPIsUpdatedImmediatelyWithoutRewritingLogin(t *testing.T) {
	m := NewMemory()
	n := time.Now().UTC()
	u := model.User{ID: ids.NewUUID(), Username: "ip-test", Status: "active"}
	if e := m.CreateUser(u); e != nil {
		t.Fatal(e)
	}
	session := model.Session{Hash: "ip-token", UserID: u.ID, ExpiresAt: n.Add(time.Hour), CreatedAt: n, LastSeenAt: n, LoginIP: "192.168.32.1", LastIP: "192.168.32.1"}
	if e := m.CreateSession(session); e != nil {
		t.Fatal(e)
	}
	if e := m.TouchSession(session.Hash, n.Add(time.Second), "8.8.8.8"); e != nil {
		t.Fatal(e)
	}
	s, e := m.GetSession(session.Hash)
	if e != nil || s.LastIP != "8.8.8.8" || s.LoginIP != session.LoginIP {
		t.Fatal(s, e)
	}
	if e = m.TouchSession(session.Hash, n.Add(2*time.Minute), ""); e != nil {
		t.Fatal(e)
	}
	s, e = m.GetSession(session.Hash)
	if e != nil || s.LastIP != "8.8.8.8" {
		t.Fatal("private requests erased public activity", s, e)
	}
}
func TestPostgresSessionIPRecoversLegacyActivityImmediately(t *testing.T) {
	p := accountPG(t)
	u := accountUser(t, p)
	n := time.Now().UTC()
	session := model.Session{Hash: ids.NewUUID(), UserID: u.ID, ExpiresAt: n.Add(time.Hour), CreatedAt: n, LastSeenAt: n, LoginIP: "192.168.32.1", LastIP: "192.168.32.1"}
	if e := p.CreateSession(session); e != nil {
		t.Fatal(e)
	}
	if e := p.TouchSession(session.Hash, n.Add(time.Second), "8.8.8.8"); e != nil {
		t.Fatal(e)
	}
	s, e := p.GetSession(session.Hash)
	if e != nil || s.LastIP != "8.8.8.8" || s.LoginIP != session.LoginIP {
		t.Fatal(s, e)
	}
	if e = p.TouchSession(session.Hash, n.Add(2*time.Minute), ""); e != nil {
		t.Fatal(e)
	}
	s, e = p.GetSession(session.Hash)
	if e != nil || s.LastIP != "8.8.8.8" {
		t.Fatal(s, e)
	}
}
