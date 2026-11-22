package sourcecatalog

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
	"git.subcult.tv/subculture-collective/vhness/internal/preservation"
)

type localTrack struct {
	name string
	data []byte
}

// localFixture is a source snapshot whose files carry Internet Archive's size
// and MD5 for the given bytes.
func localFixture(t *testing.T, id string, collections []string, tracks ...localTrack) []byte {
	t.Helper()
	files := []map[string]any{{"name": "cover.jpg", "size": "10"}}
	for _, track := range tracks {
		sum := md5.Sum(track.data)
		files = append(files, map[string]any{"name": track.name, "size": strconv.Itoa(len(track.data)), "md5": hex.EncodeToString(sum[:]), "title": strings.TrimSuffix(track.name, ".mp3")})
	}
	raw, err := json.Marshal(map[string]any{"metadata": map[string]any{"identifier": id, "collection": collections, "creator": "DJ Screw", "title": "Chapter " + id, "date": "1995"}, "files": files})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeOriginal(t *testing.T, media, collection, id string, track localTrack) {
	t.Helper()
	dir := filepath.Join(media, collection, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, track.name), track.data, 0600); err != nil {
		t.Fatal(err)
	}
}

func receiptFor(t *testing.T, collection string, files map[string][]byte) (*RecoveryReceipt, []byte) {
	t.Helper()
	listed := []RecoveredFile{}
	for path, data := range files {
		listed = append(listed, RecoveredFile{Path: path, Bytes: int64(len(data)), SHA256: domain.Digest(data)})
	}
	raw, err := json.Marshal(map[string]any{"collection": collection, "host": "nuc", "location": "/home/onnwee/Backups/" + collection, "verified_at": time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC), "files": listed})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := ParseRecoveryReceipt(raw, collection)
	if err != nil {
		t.Fatal(err)
	}
	return receipt, raw
}

func TestLocalSourceAudioReview(t *testing.T) {
	stream := testDB(t)
	ctx := context.Background()
	media := t.TempDir()
	const local = "etree-local"
	one := localTrack{"Track.. name.mp3", []byte("first original track bytes")}
	two := localTrack{"02 Second.mp3", []byte("second original track")}
	changed := localTrack{"01 Changed.mp3", []byte("source lists these bytes")}
	unbacked := localTrack{"01 Unbacked.mp3", []byte("no independent copy yet")}
	sources := map[string][]byte{
		"ready":    localFixture(t, "ready", []string{"etree"}, one, two),
		"missing":  localFixture(t, "missing", []string{"etree"}, one, two),
		"changed":  localFixture(t, "changed", []string{"etree"}, changed),
		"unbacked": localFixture(t, "unbacked", []string{"etree"}, unbacked),
	}

	// Without a source collection, a second engine collection cannot import
	// the source collection's items.
	if err := stream.Core.Collection(ctx, domain.Collection{ID: "etree-unmapped", Policy: domain.Local, Budget: 1 << 20, Reserve: 1}); err != nil {
		t.Fatal(err)
	}
	unmapped := &Store{Core: stream.Core, Collection: "etree-unmapped"}
	if err := unmapped.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := unmapped.Import(ctx, "ready", sources["ready"], 200, "", ""); err == nil {
		t.Fatal("unmapped collection imported another collection's source")
	}

	// The budget covers the whole mirror on disk, as for acquisition.
	budget := int64(2*len(one.data) + 2*len(two.data) + len(changed.data) + len(unbacked.data))
	small := int64(len(one.data))
	if err := stream.Core.Collection(ctx, domain.Collection{ID: local, Policy: domain.Local, Budget: small, Reserve: 1, SourceCollection: "etree"}); err != nil {
		t.Fatal(err)
	}
	if err := stream.Core.Collection(ctx, domain.Collection{ID: local, Policy: domain.Local, Budget: small, Reserve: 1, SourceCollection: "other"}); err == nil {
		t.Fatal("source collection changed implicitly")
	}
	store := &Store{Core: stream.Core, Collection: local, Media: media}
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	for id, raw := range sources {
		if err := store.Import(ctx, id, raw, 200, "", ""); err != nil {
			t.Fatal(id, err)
		}
	}
	writeOriginal(t, media, local, "ready", one)
	writeOriginal(t, media, local, "ready", two)
	writeOriginal(t, media, local, "missing", one)
	writeOriginal(t, media, local, "changed", localTrack{changed.name, []byte("source lists THESE bytes")})
	writeOriginal(t, media, local, "unbacked", unbacked)
	receipt, receiptRaw := receiptFor(t, local, map[string][]byte{
		"ready/" + one.name:       one.data,
		"ready/" + two.name:       two.data,
		"missing/" + one.name:     one.data,
		"missing/" + two.name:     two.data,
		"changed/" + changed.name: changed.data,
	})

	notes := "Owner approved self-hosted playback of verified originals"
	if err := store.ReviewAudio(ctx, "ready", notes); err == nil || !strings.Contains(err.Error(), "recovery receipt") {
		t.Fatal("local review accepted without independent recovery evidence", err)
	}
	store.Recovery = receipt
	if err := store.ReviewAudio(ctx, "ready", notes); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatal("originals beyond the collection budget accepted", err)
	}
	if err := stream.Core.ReviewBudget(ctx, local, small, 1, budget, 1, "Owner raised the budget to the mirrored size"); err != nil {
		t.Fatal(err)
	}

	report, err := store.ReviewAudioBatch(ctx, 100, notes)
	if err != nil || report.Reviewed != 1 || report.Retry != 3 || len(report.Skipped) != 3 {
		t.Fatal(report, err)
	}
	reasons := map[string]string{}
	for _, skip := range report.Skipped {
		if skip.State != "retry" {
			t.Fatal("pending original classified as", skip)
		}
		reasons[skip.ID] = skip.Error
	}
	for id, reason := range map[string]string{"missing": "missing/" + two.name + " is missing", "changed": "differs from the source MD5", "unbacked": "independent copy lacks"} {
		if !strings.Contains(reasons[id], reason) {
			t.Fatal(id, reasons[id])
		}
		item := "audio-" + domain.Digest([]byte(local + "\x00" + id))[:24]
		if _, err := store.Core.Transport(ctx, item, "audio", local); err == nil {
			t.Fatal("partially verified source approved", id)
		}
	}

	item := "audio-" + domain.Digest([]byte(local + "\x00ready"))[:24]
	e, err := store.Core.Transport(ctx, item, "audio", local)
	if err != nil {
		t.Fatal(err)
	}
	if e.Preservation.IndependentRecoverySHA256 != domain.Digest(receiptRaw) || e.Preservation.Snapshot != e.Representation.Snapshot || len(e.Preservation.Manifest) != 2 {
		t.Fatal("preservation approval", e.Preservation)
	}
	for _, original := range e.Preservation.Manifest {
		if original.Path != filepath.Join(local, "ready", original.File) {
			t.Fatal("manifest path", original)
		}
		if err = domain.AuthorizeTransport(e.Collection, e.Representation, e.Raw, e.Review, e.Preservation, domain.PlayAction, original.File); err != nil {
			t.Fatal(original.File, err)
		}
		f, err := preservation.Verify(media, original.Path, original.SHA256, original.Bytes)
		if err != nil {
			t.Fatal(original.File, err)
		}
		f.Close()
	}
	for _, action := range []domain.Action{domain.DownloadAction, domain.AcquireAction, domain.RelayAction, domain.ScheduleAction} {
		if domain.AuthorizeTransport(e.Collection, e.Representation, e.Raw, e.Review, e.Preservation, action, one.name) == nil || e.Review.Public {
			t.Fatal("local review granted", action)
		}
	}
	detail, err := store.Detail(ctx, "ready")
	if err != nil {
		t.Fatal(err)
	}
	var bindings []json.RawMessage
	if json.Unmarshal(detail["playback_bindings"].(json.RawMessage), &bindings) != nil || len(bindings) != 1 {
		t.Fatal("local playback binding", string(detail["playback_bindings"].(json.RawMessage)))
	}

	// The source-streaming collection and its decisions are independent.
	if _, err = stream.Detail(ctx, "ready"); err == nil {
		t.Fatal("local import leaked into the streaming collection")
	}

	// Once the original and its independent copy are complete, a single
	// review approves the source.
	writeOriginal(t, media, local, "missing", two)
	if err = store.ReviewAudio(ctx, "missing", notes); err != nil {
		t.Fatal(err)
	}

	// A free-space reserve the volume cannot keep refuses review.
	if err = stream.Core.Collection(ctx, domain.Collection{ID: "etree-reserve", Policy: domain.Local, Budget: budget, Reserve: 1 << 62, SourceCollection: "etree"}); err != nil {
		t.Fatal(err)
	}
	reserved := &Store{Core: stream.Core, Collection: "etree-reserve", Media: media}
	if err = reserved.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err = reserved.Import(ctx, "ready", sources["ready"], 200, "", ""); err != nil {
		t.Fatal(err)
	}
	writeOriginal(t, media, "etree-reserve", "ready", one)
	writeOriginal(t, media, "etree-reserve", "ready", two)
	reserved.Recovery, _ = receiptFor(t, "etree-reserve", map[string][]byte{"ready/" + one.name: one.data, "ready/" + two.name: two.data})
	if err = reserved.ReviewAudio(ctx, "ready", notes); err == nil || !strings.Contains(err.Error(), "reserve") {
		t.Fatal("free-space reserve ignored", err)
	}
}

func TestLocalSourceAudioKeepsDemocracyStreaming(t *testing.T) {
	stream := testDB(t)
	ctx := context.Background()
	for _, c := range []domain.Collection{
		{ID: "dn-local", Policy: domain.Local, Budget: 1 << 20, Reserve: 1, SourceCollection: domain.DemocracyNow},
		{ID: domain.DemocracyNow, Policy: domain.Local, Budget: 1 << 20, Reserve: 1},
	} {
		if err := stream.Core.Collection(ctx, c); err == nil {
			t.Fatal("Democracy Now! local collection accepted", c)
		}
	}
	if _, err := stream.Core.Pool.Exec(ctx, `INSERT INTO collections(id,policy,budget,reserve,source_collection) VALUES('dn-local','local_preserve',1048576,1,'democracy_now_vid')`); err == nil {
		t.Fatal("schema accepted a local Democracy Now! source collection")
	}
	media := t.TempDir()
	if err := stream.Core.Collection(ctx, domain.Collection{ID: "etree-local", Policy: domain.Local, Budget: 1 << 20, Reserve: 1, SourceCollection: "etree"}); err != nil {
		t.Fatal(err)
	}
	store := &Store{Core: stream.Core, Collection: "etree-local", Media: media}
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	track := localTrack{"01 Headlines.mp3", []byte("broadcast")}
	if err := store.Import(ctx, "dn", localFixture(t, "dn", []string{"etree", domain.DemocracyNow}, track), 200, "", ""); err != nil {
		t.Fatal(err)
	}
	writeOriginal(t, media, "etree-local", "dn", track)
	store.Recovery, _ = receiptFor(t, "etree-local", map[string][]byte{"dn/" + track.name: track.data})
	if err := store.ReviewAudio(ctx, "dn", "Must refuse local Democracy Now! audio"); err == nil || !strings.Contains(err.Error(), "source-streaming") {
		t.Fatal("Democracy Now! source preserved locally", err)
	}
}

func TestParseRecoveryReceipt(t *testing.T) {
	sha := domain.Digest([]byte("x"))
	valid := `{"collection":"c","host":"nuc","location":"/backup/c","verified_at":"2026-11-21T12:00:00Z","files":[{"path":"item/Track.. name.mp3","bytes":1,"sha256":"` + sha + `"}]}`
	receipt, err := ParseRecoveryReceipt([]byte(valid), "c")
	if err != nil || receipt.SHA256() != domain.Digest([]byte(valid)) {
		t.Fatal(receipt, err)
	}
	for name, raw := range map[string]string{
		"other collection": strings.Replace(valid, `"collection":"c"`, `"collection":"d"`, 1),
		"no host":          strings.Replace(valid, `"host":"nuc"`, `"host":" "`, 1),
		"no time":          strings.Replace(valid, `"verified_at":"2026-11-21T12:00:00Z",`, "", 1),
		"no files":         `{"collection":"c","host":"nuc","location":"/backup/c","verified_at":"2026-11-21T12:00:00Z","files":[]}`,
		"traversal":        strings.Replace(valid, "item/Track", "../Track", 1),
		"absolute":         strings.Replace(valid, "item/Track", "/item/Track", 1),
		"unclean":          strings.Replace(valid, "item/Track", "item//Track", 1),
		"bad digest":       strings.Replace(valid, sha, strings.ToUpper(sha), 1),
		"empty file":       strings.Replace(valid, `"bytes":1`, `"bytes":0`, 1),
		"unknown field":    strings.Replace(valid, `"host"`, `"extra":1,"host"`, 1),
		"trailing data":    valid + `{}`,
		"duplicate":        strings.Replace(valid, `}]}`, `},{"path":"item/Track.. name.mp3","bytes":1,"sha256":"`+sha+`"}]}`, 1),
	} {
		if _, err := ParseRecoveryReceipt([]byte(raw), "c"); err == nil {
			t.Fatal("accepted receipt:", name)
		}
	}
}
