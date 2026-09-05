// Package testutil holds the fakes and golden fixtures shared by every backend
// package's tests. MemStore is the in-memory store.Store used wherever a test
// needs persistence but not a database file; store/conformance_test.go runs the
// same suite against it and against store.SQLite, which is what keeps the two
// from drifting.
package testutil

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/store"
)

// MemStore is an in-memory store.Store. It is safe for concurrent use: every
// method takes one mutex, which is both simple and enough for tests.
//
// It reproduces the SQLite implementation's observable behaviour, not its
// internals: the same core.ErrNotFound and store.ErrAlreadyExists outcomes, the
// same orderings, and the same album_stats bookkeeping on every item write.
type MemStore struct {
	// Now is the clock for store-performed state transitions; nil means time.Now.
	Now func() int64

	mu           sync.Mutex
	meta         map[string]string
	blobs        map[string]core.Blob
	blobStates   map[string]store.BlobState
	variants     map[variantKey]store.BlobVariant
	items        map[string]core.Item // by item id
	itemsByPath  map[string]string    // rel_path -> item id
	stats        map[statKey]store.AlbumStat
	jobs         map[int64]store.Job
	nextJobID    int64
	devices      map[string]store.Device
	deviceOrder  []string
	pairingCodes map[string]store.PairingCode
}

type variantKey struct {
	hash string
	kind store.VariantKind
}

type statKey struct {
	album string
	year  int
	month int
}

// Compile-time proof that MemStore is a drop-in for the real thing.
var (
	_ core.Store  = (*MemStore)(nil)
	_ store.Store = (*MemStore)(nil)
)

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{
		meta:         map[string]string{},
		blobs:        map[string]core.Blob{},
		blobStates:   map[string]store.BlobState{},
		variants:     map[variantKey]store.BlobVariant{},
		items:        map[string]core.Item{},
		itemsByPath:  map[string]string{},
		stats:        map[statKey]store.AlbumStat{},
		jobs:         map[int64]store.Job{},
		devices:      map[string]store.Device{},
		pairingCodes: map[string]store.PairingCode{},
	}
}

func (m *MemStore) now() int64 {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now().UnixMilli()
}

// BlobState returns the state MemStore holds for a blob. SQLite exposes it
// through core.Blob's row; core.Blob has no State field, so tests that care read
// it here.
func (m *MemStore) BlobState(hash string) (store.BlobState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.blobStates[hash]
	return st, ok
}

// Meta implements store.Store.
func (m *MemStore) Meta(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.meta[key]
	if !ok {
		return "", core.ErrNotFound()
	}
	return v, nil
}

// SetMeta implements store.Store.
func (m *MemStore) SetMeta(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.meta[key] = value
	return nil
}

// BlobByHash implements core.Store.
func (m *MemStore) BlobByHash(_ context.Context, hash string) (*core.Blob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blobs[hash]
	if !ok {
		return nil, core.ErrNotFound()
	}
	return &b, nil
}

// InsertBlob implements core.Store.
func (m *MemStore) InsertBlob(_ context.Context, b core.Blob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.blobs[b.Hash]; exists {
		return fmt.Errorf("store: insert blob: %w", store.ErrAlreadyExists)
	}
	if b.OriginalReplacedAt != nil {
		v := *b.OriginalReplacedAt
		b.OriginalReplacedAt = &v
	}
	m.blobs[b.Hash] = b
	m.blobStates[b.Hash] = store.BlobStored
	return nil
}

// SetBlobState implements store.Store.
func (m *MemStore) SetBlobState(_ context.Context, hash string, state store.BlobState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.blobs[hash]; !ok {
		return core.ErrNotFound()
	}
	m.blobStates[hash] = state
	return nil
}

// SetBlobStoredHash implements store.Store. Like SQLite it touches stored_hash
// and original_replaced_at only — never Hash (D-08, invariant S4).
func (m *MemStore) SetBlobStoredHash(_ context.Context, hash, storedHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blobs[hash]
	if !ok {
		return core.ErrNotFound()
	}
	replacedAt := m.now()
	b.StoredHash = storedHash
	b.OriginalReplacedAt = &replacedAt
	m.blobs[hash] = b
	return nil
}

// SetBlobMedia implements store.Store.
func (m *MemStore) SetBlobMedia(_ context.Context, hash string, width, height int, durationMs int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blobs[hash]
	if !ok {
		return core.ErrNotFound()
	}
	b.Width, b.Height, b.DurationMs = width, height, durationMs
	m.blobs[hash] = b
	return nil
}

// InsertBlobVariant implements store.Store.
func (m *MemStore) InsertBlobVariant(_ context.Context, v store.BlobVariant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.variants[variantKey{v.Hash, v.Kind}] = v
	return nil
}

// BlobVariant implements store.Store.
func (m *MemStore) BlobVariant(_ context.Context, hash string, kind store.VariantKind) (*store.BlobVariant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.variants[variantKey{hash, kind}]
	if !ok {
		return nil, core.ErrNotFound()
	}
	return &v, nil
}

// BlobVariantsByHash implements store.Store.
func (m *MemStore) BlobVariantsByHash(_ context.Context, hash string) ([]store.BlobVariant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.BlobVariant
	for k, v := range m.variants {
		if k.hash == hash {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out, nil
}

// InsertItem implements core.Store, keeping album stats in step exactly as the
// SQLite transaction does (invariant S3).
func (m *MemStore) InsertItem(_ context.Context, it core.Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blobs[it.Hash]
	if !ok {
		return fmt.Errorf("store: insert item: unknown blob %q: %w", it.Hash, core.ErrNotFound())
	}
	if _, exists := m.items[it.ItemID]; exists {
		return fmt.Errorf("store: insert item: %w", store.ErrAlreadyExists)
	}
	if _, exists := m.itemsByPath[it.RelPath]; exists {
		return fmt.Errorf("store: insert item: %w", store.ErrAlreadyExists)
	}

	m.items[it.ItemID] = it
	m.itemsByPath[it.RelPath] = it.ItemID

	key := statKey{it.Album, it.Year, it.Month}
	st := m.stats[key]
	st.Album, st.Year, st.Month = it.Album, it.Year, it.Month
	st.ItemCount++
	st.SizeBytes += b.SizeBytes
	if it.CapturedAtMs > st.LatestCapturedAtMs {
		st.LatestCapturedAtMs = it.CapturedAtMs
	}
	m.stats[key] = st
	return nil
}

// ItemByPath implements core.Store.
func (m *MemStore) ItemByPath(_ context.Context, relPath string) (*core.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.itemsByPath[relPath]
	if !ok {
		return nil, core.ErrNotFound()
	}
	it := m.items[id]
	return &it, nil
}

// ItemByID implements store.Store.
func (m *MemStore) ItemByID(_ context.Context, itemID string) (*core.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[itemID]
	if !ok {
		return nil, core.ErrNotFound()
	}
	return &it, nil
}

// ItemsByHash implements store.Store.
func (m *MemStore) ItemsByHash(_ context.Context, hash string) ([]core.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []core.Item
	for _, it := range m.items {
		if it.Hash == hash {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ItemID < out[j].ItemID })
	return out, nil
}

// DeleteItem implements store.Store.
func (m *MemStore) DeleteItem(_ context.Context, itemID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[itemID]
	if !ok {
		return core.ErrNotFound()
	}
	b := m.blobs[it.Hash]

	delete(m.items, itemID)
	delete(m.itemsByPath, it.RelPath)

	key := statKey{it.Album, it.Year, it.Month}
	st := m.stats[key]
	st.ItemCount--
	st.SizeBytes -= b.SizeBytes
	if st.ItemCount <= 0 {
		delete(m.stats, key)
		return nil
	}
	// The removed item may have been the newest, so recompute rather than track.
	st.LatestCapturedAtMs = 0
	for _, other := range m.items {
		if other.Album == it.Album && other.Year == it.Year && other.Month == it.Month &&
			other.CapturedAtMs > st.LatestCapturedAtMs {
			st.LatestCapturedAtMs = other.CapturedAtMs
		}
	}
	m.stats[key] = st
	return nil
}

// AlbumStat implements store.Store.
func (m *MemStore) AlbumStat(_ context.Context, album string, year, month int) (*store.AlbumStat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.stats[statKey{album, year, month}]
	if !ok {
		return nil, core.ErrNotFound()
	}
	return &st, nil
}

// AlbumStats implements store.Store.
func (m *MemStore) AlbumStats(_ context.Context) ([]store.AlbumStat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.AlbumStat, 0, len(m.stats))
	for _, st := range m.stats {
		out = append(out, st)
	}
	sortAlbumStats(out)
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// DeriveAlbumStats implements store.Store, recomputing from items and blobs the
// way homesinkd fsck does.
func (m *MemStore) DeriveAlbumStats(_ context.Context) ([]store.AlbumStat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	derived := map[statKey]store.AlbumStat{}
	for _, it := range m.items {
		key := statKey{it.Album, it.Year, it.Month}
		st := derived[key]
		st.Album, st.Year, st.Month = it.Album, it.Year, it.Month
		st.ItemCount++
		st.SizeBytes += m.blobs[it.Hash].SizeBytes
		if it.CapturedAtMs > st.LatestCapturedAtMs {
			st.LatestCapturedAtMs = it.CapturedAtMs
		}
		derived[key] = st
	}
	out := make([]store.AlbumStat, 0, len(derived))
	for _, st := range derived {
		out = append(out, st)
	}
	sortAlbumStats(out)
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func sortAlbumStats(s []store.AlbumStat) {
	sort.Slice(s, func(i, j int) bool {
		switch {
		case s[i].Album != s[j].Album:
			return s[i].Album < s[j].Album
		case s[i].Year != s[j].Year:
			return s[i].Year > s[j].Year
		default:
			return s[i].Month > s[j].Month
		}
	})
}

// EnqueueJob implements store.Store.
func (m *MemStore) EnqueueJob(_ context.Context, j store.NewJob) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	priority := j.Priority
	if priority == 0 {
		priority = store.PriorityTranscode
		if j.Kind == store.JobThumbnail {
			priority = store.PriorityThumbnail
		}
	}
	m.nextJobID++
	job := store.Job{
		JobID:           m.nextJobID,
		Kind:            j.Kind,
		Payload:         j.Payload,
		Priority:        priority,
		State:           store.JobQueued,
		NextAttemptAtMs: j.NextAttemptAtMs,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	m.jobs[job.JobID] = job
	return job.JobID, nil
}

// ClaimJob implements store.Store.
func (m *MemStore) ClaimJob(_ context.Context) (*store.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()

	var best *store.Job
	for id := range m.jobs {
		job := m.jobs[id]
		if job.State != store.JobQueued || job.NextAttemptAtMs > now {
			continue
		}
		if best == nil || jobBefore(job, *best) {
			candidate := job
			best = &candidate
		}
	}
	if best == nil {
		return nil, core.ErrNotFound()
	}

	best.State = store.JobRunning
	best.Attempts++
	best.UpdatedAt = now
	m.jobs[best.JobID] = *best
	claimed := *best
	return &claimed, nil
}

func jobBefore(a, b store.Job) bool {
	switch {
	case a.Priority != b.Priority:
		return a.Priority < b.Priority
	case a.NextAttemptAtMs != b.NextAttemptAtMs:
		return a.NextAttemptAtMs < b.NextAttemptAtMs
	default:
		return a.JobID < b.JobID
	}
}

// CompleteJob implements store.Store.
func (m *MemStore) CompleteJob(_ context.Context, jobID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return core.ErrNotFound()
	}
	job.State = store.JobDone
	job.LastError = ""
	job.UpdatedAt = m.now()
	m.jobs[jobID] = job
	return nil
}

// FailJob implements store.Store.
func (m *MemStore) FailJob(_ context.Context, jobID int64, cause string,
	nextAttemptAtMs int64, maxAttempts int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return core.ErrNotFound()
	}
	job.LastError = cause
	job.UpdatedAt = m.now()
	if job.Attempts >= maxAttempts {
		job.State = store.JobFailed
		job.NextAttemptAtMs = 0
	} else {
		job.State = store.JobQueued
		job.NextAttemptAtMs = nextAttemptAtMs
	}
	m.jobs[jobID] = job
	return nil
}

// JobByID implements store.Store.
func (m *MemStore) JobByID(_ context.Context, jobID int64) (*store.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok {
		return nil, core.ErrNotFound()
	}
	return &job, nil
}

// JobsByState implements store.Store.
func (m *MemStore) JobsByState(_ context.Context, state store.JobState, limit int) ([]store.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Job
	for _, job := range m.jobs {
		if job.State == state {
			out = append(out, job)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JobID < out[j].JobID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// RequeueRunningJobs implements store.Store.
func (m *MemStore) RequeueRunningJobs(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	moved := 0
	for id, job := range m.jobs {
		if job.State != store.JobRunning {
			continue
		}
		job.State = store.JobQueued
		job.UpdatedAt = now
		m.jobs[id] = job
		moved++
	}
	return moved, nil
}

// InsertDevice implements store.Store.
func (m *MemStore) InsertDevice(_ context.Context, d store.Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.devices[d.DeviceID]; exists {
		return fmt.Errorf("store: insert device: %w", store.ErrAlreadyExists)
	}
	m.devices[d.DeviceID] = d
	m.deviceOrder = append(m.deviceOrder, d.DeviceID)
	return nil
}

// DeviceByID implements store.Store.
func (m *MemStore) DeviceByID(_ context.Context, deviceID string) (*store.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[deviceID]
	if !ok {
		return nil, core.ErrNotFound()
	}
	return &d, nil
}

// DeviceByTokenHash implements store.Store; revoked devices are not found.
func (m *MemStore) DeviceByTokenHash(_ context.Context, tokenHash string) (*store.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range m.deviceOrder {
		d := m.devices[id]
		if d.TokenHash == tokenHash && !d.Revoked() {
			return &d, nil
		}
	}
	return nil, core.ErrNotFound()
}

// ListDevices implements store.Store.
func (m *MemStore) ListDevices(_ context.Context) ([]store.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.Device, 0, len(m.devices))
	for _, id := range m.deviceOrder {
		out = append(out, m.devices[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].DeviceID < out[j].DeviceID
	})
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// RevokeDevice implements store.Store.
func (m *MemStore) RevokeDevice(_ context.Context, deviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[deviceID]
	if !ok {
		return core.ErrNotFound()
	}
	if d.Revoked() {
		return nil
	}
	d.RevokedAt = m.now()
	m.devices[deviceID] = d
	return nil
}

// TouchDevice implements store.Store.
func (m *MemStore) TouchDevice(_ context.Context, deviceID string, appVersionCode int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[deviceID]
	if !ok {
		return core.ErrNotFound()
	}
	d.LastSeenAt = m.now()
	if appVersionCode != 0 {
		d.AppVersionCode = appVersionCode
	}
	m.devices[deviceID] = d
	return nil
}

// InsertPairingCode implements store.Store.
func (m *MemStore) InsertPairingCode(_ context.Context, pc store.PairingCode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.pairingCodes[pc.CodeHash]; exists {
		return fmt.Errorf("store: insert pairing code: %w", store.ErrAlreadyExists)
	}
	m.pairingCodes[pc.CodeHash] = pc
	return nil
}

// PairingCodeByHash implements store.Store.
func (m *MemStore) PairingCodeByHash(_ context.Context, codeHash string) (*store.PairingCode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pc, ok := m.pairingCodes[codeHash]
	if !ok {
		return nil, core.ErrNotFound()
	}
	return &pc, nil
}

// MarkPairingCodeUsed implements store.Store; a second redemption of one code is
// store.ErrAlreadyExists, so exactly one racing device wins (D-01, single use).
func (m *MemStore) MarkPairingCodeUsed(_ context.Context, codeHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pc, ok := m.pairingCodes[codeHash]
	if !ok {
		return core.ErrNotFound()
	}
	if pc.Used() {
		return store.ErrAlreadyExists
	}
	pc.UsedAtMs = m.now()
	m.pairingCodes[codeHash] = pc
	return nil
}

// IncrementPairingCodeAttempts implements store.Store.
func (m *MemStore) IncrementPairingCodeAttempts(_ context.Context, codeHash string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pc, ok := m.pairingCodes[codeHash]
	if !ok {
		return 0, core.ErrNotFound()
	}
	pc.Attempts++
	m.pairingCodes[codeHash] = pc
	return pc.Attempts, nil
}

// DeletePairingCodesBefore implements store.Store.
func (m *MemStore) DeletePairingCodesBefore(_ context.Context, expiresBeforeMs int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := 0
	for hash, pc := range m.pairingCodes {
		if pc.ExpiresAtMs < expiresBeforeMs {
			delete(m.pairingCodes, hash)
			removed++
		}
	}
	return removed, nil
}

// String is a compact dump for test failure messages.
func (m *MemStore) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "MemStore{blobs:%d items:%d stats:%d jobs:%d devices:%d}",
		len(m.blobs), len(m.items), len(m.stats), len(m.jobs), len(m.devices))
	return b.String()
}
