package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	_ "modernc.org/sqlite"
	"scp-harness/internal/ledger"
	"scp-harness/internal/model"
)

//go:embed schema.sql
var schema string

// Store is the single concrete SQLite database. There is no storage interface.
// A v0 transaction loads a typed state, checks its complete referential and
// conservation invariants, then atomically publishes it. Journal/blocker rows
// are separate to make the mandated recovery records directly inspectable.
type Store struct{ DB *sql.DB }

// OpenReadOnly uses deferred read transactions, never a RESERVED writer lock.
// Observation failures must not enter Update or EmergencyBlock.
func OpenReadOnly(path string) (*Store, error) {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return nil, storage(e)
	}
	p := filepath.ToSlash(absolute)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro&_pragma=busy_timeout(1000)&_txlock=deferred"}
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, storage(e)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db}
	var version int
	if e = db.QueryRow("SELECT version FROM schema_version").Scan(&version); e != nil {
		db.Close()
		return nil, storage(e)
	}
	if version != 2 {
		db.Close()
		return nil, model.Err("CORE_INCONSISTENT", "unsupported database schema %d", version)
	}
	return s, nil
}

func Open(path string, init bool) (*Store, error) {
	if init {
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return nil, storage(e)
		}
	} else {
		if _, e := os.Stat(path); e != nil {
			return nil, storage(e)
		}
	}
	db, e := sql.Open("sqlite", filepath.ToSlash(path)+"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)&_txlock=immediate")
	if e != nil {
		return nil, storage(e)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db}
	fail := func(e error) (*Store, error) { db.Close(); return nil, e }
	var fk int
	if e = db.QueryRow("PRAGMA foreign_keys").Scan(&fk); e != nil || fk != 1 {
		return fail(storage(fmt.Errorf("foreign keys disabled: %v", e)))
	}
	if init {
		tx, e := db.Begin()
		if e != nil {
			return fail(storage(e))
		}
		defer tx.Rollback()
		if _, e = tx.Exec(schema); e != nil {
			return fail(storage(e))
		}
		b, _ := json.Marshal(model.NewState())
		if _, e = tx.Exec("INSERT OR IGNORE INTO core_state(id,body) VALUES(1,?)", string(b)); e != nil {
			return fail(storage(e))
		}
		if e = tx.Commit(); e != nil {
			return fail(storage(e))
		}
	}
	var v int
	if e = db.QueryRow("SELECT version FROM schema_version").Scan(&v); e != nil {
		return fail(storage(e))
	}
	if v != 2 {
		return fail(model.Err("CORE_INCONSISTENT", "unsupported database schema %d", v))
	}
	if _, e = s.Read(); e != nil {
		return fail(e)
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func storage(e error) error   { return model.Err("STORAGE_FAILURE", "SQLite: %v", e) }
func load(tx *sql.Tx) (*model.State, error) {
	var b []byte
	if e := tx.QueryRow("SELECT body FROM core_state WHERE id=1").Scan(&b); e != nil {
		return nil, storage(e)
	}
	state := model.NewState()
	if e := json.Unmarshal(b, state); e != nil {
		return nil, model.Err("CORE_INCONSISTENT", "decode Core state: %v", e)
	}
	state.Blockers = map[string]*model.Blocker{}
	state.Journals = map[string]*model.Journal{}
	for _, table := range []string{"repo_update_journal", "runtime_blockers"} {
		rows, e := tx.Query("SELECT body FROM " + table + " ORDER BY id")
		if e != nil {
			return nil, storage(e)
		}
		for rows.Next() {
			var data []byte
			if e = rows.Scan(&data); e != nil {
				rows.Close()
				return nil, storage(e)
			}
			if table == "runtime_blockers" {
				var v model.Blocker
				e = json.Unmarshal(data, &v)
				state.Blockers[v.ID] = &v
			} else {
				var v model.Journal
				e = json.Unmarshal(data, &v)
				state.Journals[v.ID] = &v
			}
			if e != nil {
				rows.Close()
				return nil, model.Err("CORE_INCONSISTENT", "decode %s: %v", table, e)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, storage(e)
		}
	}
	return state, nil
}
func (s *Store) Read() (*model.State, error) {
	tx, e := s.DB.BeginTx(context.Background(), nil)
	if e != nil {
		return nil, storage(e)
	}
	defer tx.Rollback()
	state, e := load(tx)
	if e != nil {
		return nil, e
	}
	if e = Validate(state); e != nil {
		return nil, e
	}
	return state, nil
}
func (s *Store) Update(fn func(*model.State) error) (result error) {
	defer func() {
		if model.Exit(model.Code(result)) == 5 {
			_ = s.EmergencyBlock(model.Code(result), result.Error())
		}
	}()
	tx, e := s.DB.BeginTx(context.Background(), nil)
	if e != nil {
		return storage(e)
	}
	defer tx.Rollback()
	state, e := load(tx)
	if e != nil {
		return e
	}
	if e = Validate(state); e != nil {
		return e
	}
	beforeBytes, _ := json.Marshal(state)
	before := model.NewState()
	if e = json.Unmarshal(beforeBytes, before); e != nil {
		return model.Err("INTERNAL_ERROR", "snapshot: %v", e)
	}
	if e = fn(state); e != nil {
		return e
	}
	if e = ledger.Refresh(state); e != nil {
		return e
	}
	if e = Validate(state); e != nil {
		return e
	}
	if e = immutable(before, state); e != nil {
		return e
	}
	for id, b := range state.Blockers {
		data, _ := json.Marshal(b)
		if _, e = tx.Exec("INSERT INTO runtime_blockers(id,body) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", id, string(data)); e != nil {
			return storage(e)
		}
	}
	for id, j := range state.Journals {
		data, _ := json.Marshal(j)
		if _, e = tx.Exec("INSERT INTO repo_update_journal(id,body) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", id, string(data)); e != nil {
			return storage(e)
		}
	}
	state.Blockers = nil
	state.Journals = nil
	b, e := json.Marshal(state)
	if e != nil {
		return model.Err("INTERNAL_ERROR", "encode state: %v", e)
	}
	if _, e = tx.Exec("UPDATE core_state SET body=? WHERE id=1", string(b)); e != nil {
		return storage(e)
	}
	if e = tx.Commit(); e != nil {
		return storage(e)
	}
	return nil
}

// A damaged authority snapshot must not prevent a best-effort durable GLOBAL
// diagnostic. This writes only runtime_blockers and never repairs Core state.
func (s *Store) EmergencyBlock(kind, message string) error {
	if kind != "STORAGE_FAILURE" && kind != "CORE_INCONSISTENT" {
		return model.Err("INTERNAL_ERROR", "invalid fail-stop kind")
	}
	rows, e := s.DB.Query("SELECT body FROM runtime_blockers")
	if e != nil {
		return storage(e)
	}
	exists := false
	for rows.Next() {
		var data []byte
		if e = rows.Scan(&data); e != nil {
			rows.Close()
			return storage(e)
		}
		var b model.Blocker
		if json.Unmarshal(data, &b) == nil && b.Kind == kind && b.Resolved == nil {
			exists = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return storage(e)
	}
	if exists {
		return nil
	}
	b := model.Blocker{ID: model.ID(), Kind: kind, Scope: "GLOBAL", Message: message, Created: model.Now()}
	data, _ := json.Marshal(b)
	if _, e = s.DB.Exec("INSERT INTO runtime_blockers(id,body) VALUES(?,?)", b.ID, string(data)); e != nil {
		return storage(e)
	}
	return nil
}
func immutable(a, b *model.State) error {
	if e := immutableChanges(a, b); e != nil {
		return e
	}
	for id, old := range a.Options {
		n := b.Options[id]
		if n == nil {
			return model.Err("CORE_INCONSISTENT", "Option deletion")
		}
		x, y := *old, *n
		x.Status = y.Status
		x.CloseReason = y.CloseReason
		x.Remaining = y.Remaining
		if x != y {
			return model.Err("CORE_INCONSISTENT", "immutable Option altered")
		}
		if old.Status == "CLOSED" && n.Status != "CLOSED" {
			return model.Err("CORE_INCONSISTENT", "Option reopened")
		}
	}
	for id, old := range a.Artifacts {
		if !reflect.DeepEqual(old, b.Artifacts[id]) {
			return model.Err("CORE_INCONSISTENT", "immutable Artifact altered")
		}
	}
	for id, old := range a.Claims {
		if !reflect.DeepEqual(old, b.Claims[id]) {
			return model.Err("CORE_INCONSISTENT", "immutable Claim altered")
		}
	}
	for id, old := range a.Attempts {
		n := b.Attempts[id]
		if n == nil {
			return model.Err("CORE_INCONSISTENT", "Attempt deletion")
		}
		if old.Objective != n.Objective || old.ChangeID != n.ChangeID || old.ID != n.ID || old.TaskID != n.TaskID || old.Operation != n.Operation || old.TargetType != n.TargetType || old.TargetID != n.TargetID || old.AnchorID != n.AnchorID || old.AnchorType != n.AnchorType || old.Lease != n.Lease || old.SHA != n.SHA || old.Revision != n.Revision || old.Actor != n.Actor || old.Profile != n.Profile {
			return model.Err("CORE_INCONSISTENT", "Attempt identity changed")
		}
	}
	return nil
}
func Validate(s *model.State) error {
	bad := func(msg string) error { return model.Err("CORE_INCONSISTENT", "%s", msg) }
	if s.Changes == nil || s.CIRuns == nil || s.Tasks == nil || s.Accounts == nil || s.Options == nil || s.Leases == nil || s.Claims == nil || s.Artifacts == nil || s.Attempts == nil || s.Pending == nil || s.Exploration == nil || s.Reviews == nil || s.Interrupts == nil || s.Cancellations == nil || s.RepositoryIdentities == nil {
		return bad("missing state maps")
	}
	for id, v := range s.Tasks {
		if v == nil || id != v.ID || s.Exploration[id] == nil {
			return bad("invalid Task/runtime record")
		}
	}
	for id, v := range s.Options {
		if v == nil || id != v.ID {
			return bad("invalid Option record")
		}
	}
	for id, v := range s.Artifacts {
		if v == nil || id != v.ID {
			return bad("invalid Artifact record")
		}
	}
	for id, v := range s.Claims {
		if v == nil || id != v.ID {
			return bad("invalid Claim record")
		}
	}
	for id, v := range s.Attempts {
		if v == nil || id != v.ID {
			return bad("invalid Attempt record")
		}
		switch v.Status {
		case "PREPARING", "RUNNING", "RETURNED", "TIMED_OUT", "INTERRUPTED", "CRASHED", "TERMINATED":
		default:
			return bad("invalid Attempt status")
		}
	}
	for _, v := range s.Accounts {
		if v == nil {
			return bad("null resource account")
		}
	}
	for _, v := range s.Leases {
		if v == nil {
			return bad("null resource lease")
		}
	}
	for id, p := range s.Pending {
		if p == nil || p.TaskID != id || s.Tasks[id] == nil || !Subject(s, id, p.TargetType, p.TargetID) {
			return bad("invalid pending target")
		}
	}
	if s.Blockers == nil {
		s.Blockers = map[string]*model.Blocker{}
	}
	if s.Journals == nil {
		s.Journals = map[string]*model.Journal{}
	}
	repos := map[string]bool{}
	for id, t := range s.Tasks {
		if id != t.ID || s.Accounts[id] == nil || s.Accounts[id].Parent != "" || s.Accounts[id].TaskID != id {
			return bad("invalid Task root")
		}
		if t.Status == "ACTIVE" {
			identity := s.RepositoryIdentities[id]
			if identity == "" {
				return bad("missing repository identity")
			}
			if repos[identity] {
				return bad("duplicate ACTIVE repository binding")
			}
			repos[identity] = true
		} else if t.Status != "SUSPENDED" && t.Status != "CLOSED" {
			return bad("invalid Task status")
		}
	}
	for id, o := range s.Options {
		a := s.Accounts[id]
		if id != o.ID || s.Tasks[o.TaskID] == nil || a == nil || a.TaskID != o.TaskID || a.Parent != o.Parent || a.Remaining != o.Remaining || o.Status != "OPEN" && o.Status != "CLOSED" {
			return bad("invalid Option/account")
		}
	}
	for id, a := range s.Accounts {
		if s.Tasks[a.TaskID] == nil || a.Remaining < 0 {
			return bad("invalid account")
		}
		if id != a.TaskID && s.Options[id] == nil {
			return bad("orphan account")
		}
		if a.Parent != "" && (s.Accounts[a.Parent] == nil || s.Accounts[a.Parent].TaskID != a.TaskID) {
			return bad("cross-task/dangling resource parent")
		}
		seen := map[string]bool{}
		for p := id; p != ""; p = s.Accounts[p].Parent {
			if seen[p] || s.Accounts[p] == nil {
				return bad("resource cycle")
			}
			seen[p] = true
		}
	}
	for _, e := range s.Edges {
		if s.Options[e.Parent] == nil || s.Options[e.Child] == nil {
			return bad("dangling Option edge")
		}
	}
	for _, a := range s.Artifacts {
		if s.Options[a.Anchor] == nil || s.Options[a.Anchor].TaskID != a.TaskID || s.Attempts[a.AttemptID] == nil || s.Attempts[a.AttemptID].TaskID != a.TaskID {
			return bad("dangling Artifact provenance")
		}
	}
	active := 0
	for _, a := range s.Attempts {
		if s.Tasks[a.TaskID] == nil || s.Accounts[a.AnchorID] == nil || s.Accounts[a.AnchorID].TaskID != a.TaskID || !Subject(s, a.TaskID, a.TargetType, a.TargetID) {
			return bad("dangling Attempt")
		}
		if a.Active() {
			active++
			if s.Leases[a.ID] == nil {
				return bad("active Attempt without lease")
			}
		} else if s.Leases[a.ID] != nil {
			return bad("terminal Attempt with outstanding lease")
		}
	}
	if active > 1 {
		return bad("multiple active Attempts")
	}
	for id, t := range s.Tasks {
		if t.Status != "ACTIVE" && !TaskQuiescent(s, id) {
			return bad("inactive Task has active execution, outstanding lease or execution slot")
		}
	}
	for _, c := range s.Claims {
		if !Subject(s, c.TaskID, c.SubjectType, c.SubjectID) {
			return bad("dangling Claim")
		}
	}
	for _, j := range s.Journals {
		if s.Tasks[j.TaskID] == nil || s.Artifacts[j.ArtifactID] == nil {
			return bad("dangling promotion journal")
		}
	}
	if e := validateChanges(s); e != nil {
		return e
	}
	// Refresh on a copy verifies the persisted projections were not corrupted.
	expected := map[string]model.Resources{}
	for id, t := range s.Tasks {
		expected[id] = t.Resources
	}
	if e := ledger.Refresh(s); e != nil {
		return e
	}
	for id, t := range s.Tasks {
		if expected[id] != t.Resources {
			return bad("resource projection mismatch")
		}
	}
	return nil
}

// TaskQuiescent includes protected-test leases and the between-step slot, not
// just worker Attempts. Both final control transactions and validation use it.
func TaskQuiescent(s *model.State, taskID string) bool {
	if s.SlotTask == taskID {
		return false
	}
	for _, a := range s.Attempts {
		if a.TaskID == taskID && a.Active() {
			return false
		}
	}
	for _, l := range s.Leases {
		if l.TaskID == taskID {
			return false
		}
	}
	return true
}
func Subject(s *model.State, task, kind, id string) bool {
	switch kind {
	case "TASK":
		return id == task && s.Tasks[id] != nil
	case "OPTION":
		return s.Options[id] != nil && s.Options[id].TaskID == task
	case "ARTIFACT":
		return s.Artifacts[id] != nil && s.Artifacts[id].TaskID == task
	}
	return false
}
