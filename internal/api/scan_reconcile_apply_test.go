package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"resolvarr/internal/core"
	"resolvarr/internal/core/engine"
)

// End-to-end tests for runReconcile's apply pass against a fake Sonarr
// and a fake qBittorrent. They pin the two safety properties the
// automatic (webhook) path depends on: a season pack is only moved when
// EVERY episode in it is redundant, and a torrent already sitting in the
// target category is not "moved" again.

const (
	hashPack    = "pppppppppppppppppppppppppppppppppppppppp" // ep 101 redundant, ep 102 needed
	hashSingle  = "ssssssssssssssssssssssssssssssssssssssss" // ep 103 redundant
	hashAlready = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // ep 104 redundant, already moved
)

// fakeSonarrForReconcile serves the handful of endpoints runReconcile
// reads for series 7. Everything else answers an empty list — the
// history / manual-import / download-client reads are best-effort.
func fakeSonarrForReconcile(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/queue":
			row := func(hash string, ep int) map[string]any {
				return map[string]any{
					"downloadId": hash, "title": "Show.S01." + hash[:1], "status": "completed",
					"trackedDownloadState": "importBlocked", "customFormatScore": 50,
					"seriesId": 7, "episodeId": ep,
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"records": []any{
				row(hashPack, 101), row(hashPack, 102),
				row(hashSingle, 103),
				row(hashAlready, 104),
			}})
		case "/api/v3/episode":
			// 102 has no file — the pack is the only copy of it.
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 101, "episodeFileId": 1001},
				{"id": 102, "episodeFileId": 0},
				{"id": 103, "episodeFileId": 1003},
				{"id": 104, "episodeFileId": 1004},
			})
		case "/api/v3/episodefile":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1001, "seriesId": 7, "customFormatScore": 100},
				{"id": 1003, "seriesId": 7, "customFormatScore": 100},
				{"id": 1004, "seriesId": 7, "customFormatScore": 100},
			})
		default:
			_, _ = w.Write([]byte("[]"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeQbitWithCategories holds hash → current category and records
// every setCategory call.
type fakeQbitWithCategories struct {
	mu   sync.Mutex
	cats map[string]string
	sets []string // "hash=category"
	srv  *httptest.Server
}

func newFakeQbitWithCategories(t *testing.T, cats map[string]string) *fakeQbitWithCategories {
	t.Helper()
	q := &fakeQbitWithCategories{cats: cats}
	q.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q.mu.Lock()
		defer q.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/auth/login"):
			_, _ = w.Write([]byte("Ok."))
		case strings.HasSuffix(r.URL.Path, "/torrents/info"):
			h := strings.ToLower(r.URL.Query().Get("hashes"))
			cat, ok := q.cats[h]
			if !ok {
				_, _ = w.Write([]byte("[]"))
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"hash": h, "name": "x", "category": cat}})
		case strings.HasSuffix(r.URL.Path, "/torrents/setCategory"):
			_ = r.ParseForm()
			h, c := r.PostForm.Get("hashes"), r.PostForm.Get("category")
			q.sets = append(q.sets, h+"="+c)
			q.cats[strings.ToLower(h)] = c
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(q.srv.Close)
	return q
}

func runReconcileApplyForTest(t *testing.T, applyItems []string) (*scanResponse, *fakeQbitWithCategories) {
	t.Helper()
	sonarr := fakeSonarrForReconcile(t)
	q := newFakeQbitWithCategories(t, map[string]string{
		hashPack:    "tv",
		hashSingle:  "tv",
		hashAlready: "cleanup",
	})
	inst := &core.Instance{ID: "s1", Name: "Sonarr", Type: "sonarr", URL: sonarr.URL, APIKey: "k"}
	cfg := core.Config{
		Instances:     []core.Instance{*inst},
		QbitInstances: []core.QbitInstance{{ID: "q1", Name: "qBit", URL: q.srv.URL}},
	}
	s := &Server{App: &core.App{HTTPClient: http.DefaultClient}}
	resp, apiErr := s.runReconcile(context.Background(), cfg, inst, "sonarr", scanRunRequest{
		InstanceID:          "s1",
		Action:              "reconcile",
		Mode:                "apply",
		ReconcileTargets:    []scanReconcileTarget{{QbitInstanceID: "q1", PostCategory: "cleanup"}},
		ReconcileApplyItems: applyItems,
	})
	if apiErr != nil {
		t.Fatalf("runReconcile: %v", apiErr.Message)
	}
	return resp, q
}

func TestRunReconcileApply_LeavesPartlyNeededPackAlone(t *testing.T) {
	resp, q := runReconcileApplyForTest(t, nil)

	// Only the single redundant download moves. The pack is redundant
	// for episode 101 but is the only copy of 102 — moving it to the
	// cleanup category would let the user's cleanup delete 102.
	if len(q.sets) != 1 || q.sets[0] != hashSingle+"=cleanup" {
		t.Fatalf("setCategory calls = %v, want only %s=cleanup", q.sets, hashSingle)
	}
	for _, row := range resp.Reconcile {
		if row.DownloadID == hashPack && row.Status == "recategorised" {
			t.Errorf("pack row for episode %d reported recategorised", row.EpisodeID)
		}
	}
	// Per-row verdicts are still reported honestly.
	var packVerdicts []string
	for _, row := range resp.Reconcile {
		if row.DownloadID == hashPack {
			packVerdicts = append(packVerdicts, row.Status)
		}
	}
	want := []string{string(engine.StuckRedundant), string(engine.StuckNeedsAttention)}
	if strings.Join(packVerdicts, ",") != strings.Join(want, ",") {
		t.Errorf("pack verdicts = %v, want %v", packVerdicts, want)
	}
}

func TestRunReconcileApply_AlreadyInCategoryIsNotMovedAgain(t *testing.T) {
	resp, q := runReconcileApplyForTest(t, nil)

	for _, s := range q.sets {
		if strings.HasPrefix(s, hashAlready) {
			t.Errorf("torrent already in the target category was re-set: %s", s)
		}
	}
	if resp.Totals.ReconcileRecategorised != 1 {
		t.Errorf("ReconcileRecategorised = %d, want 1 (the already-moved one must not count)", resp.Totals.ReconcileRecategorised)
	}
	d := summariseStuckReconcile(resp)
	if d.Stuck != 3 || d.Moved != 1 || d.AlreadyMoved != 1 || d.NeedsAttention != 1 || d.Failed != 0 {
		t.Errorf("summary = %+v, want 3 stuck downloads: 1 moved, 1 already moved, 1 needs attention", d)
	}
	if len(d.Moves) != 1 || d.Moves[0].Category != "cleanup" {
		t.Errorf("moves = %+v, want the one real move", d.Moves)
	}
}

func TestRunReconcileApply_ExplicitSelectionIsHonoured(t *testing.T) {
	// The user ticked the pack on the Stuck downloads page — their call.
	_, q := runReconcileApplyForTest(t, []string{hashPack})
	if len(q.sets) != 1 || q.sets[0] != hashPack+"=cleanup" {
		t.Fatalf("setCategory calls = %v, want only the selected pack", q.sets)
	}
}
