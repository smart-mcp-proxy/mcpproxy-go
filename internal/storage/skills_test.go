package storage_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage/purgetest"
)

func hexDigest(c byte) string { return strings.Repeat(string(c), 64) }

func skillRec(server, uri string, current, approved []storage.SkillFile) *storage.SkillApprovalRecord {
	r := &storage.SkillApprovalRecord{
		ServerName: server, SkillURI: uri, SkillPath: "x", Name: "x", Description: "d",
		Status: storage.SkillStatusPending, Listed: true,
		Current:   storage.SkillContent{Frontmatter: json.RawMessage(`{"name":"x"}`), Files: current},
		FirstSeen: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if approved != nil {
		r.Approved = &storage.SkillContent{Files: approved}
	}
	return r
}

func textFile(rel, hex string) storage.SkillFile {
	return storage.SkillFile{RelPath: rel, URI: "skill://x/" + rel, Digest: "sha256:" + hex, Size: 3, Text: true, Stored: true}
}

func TestSkillApproval_RoundTrip(t *testing.T) {
	m := newPurgeTestManager(t)
	r := skillRec("s", "skill://x/SKILL.md", nil, nil)
	r.Scan = &storage.SkillScan{Verdict: "clean", Complete: true, Findings: []storage.SkillScanFinding{{File: "a", Offset: 3, Rule: "r", Severity: "high", Evidence: "e"}}}
	require.NoError(t, m.SaveSkillApproval(r))

	got, err := m.GetSkillApproval("s", "skill://x/SKILL.md")
	require.NoError(t, err)
	assert.Nil(t, got.Approved, "nil Approved stays nil")
	assert.Equal(t, r.Scan, got.Scan)

	r.Approved = &storage.SkillContent{Files: []storage.SkillFile{textFile("SKILL.md", hexDigest('a'))}}
	require.NoError(t, m.SaveSkillApproval(r))
	got, err = m.GetSkillApproval("s", "skill://x/SKILL.md")
	require.NoError(t, err)
	require.NotNil(t, got.Approved)
	assert.Len(t, got.Approved.Files, 1)

	_, err = m.GetSkillApproval("s", "skill://nope/SKILL.md")
	assert.True(t, errors.Is(err, storage.ErrSkillApprovalNotFound))

	require.NoError(t, m.DeleteSkillApproval("s", "skill://x/SKILL.md"))
	_, err = m.GetSkillApproval("s", "skill://x/SKILL.md")
	assert.True(t, errors.Is(err, storage.ErrSkillApprovalNotFound))
}

// Server names cannot contain ':' (config validation), but listing must not
// rely on that alone: "a" must never see rows of "a:b".
func TestSkillApproval_ListExactServer(t *testing.T) {
	m := newPurgeTestManager(t)
	require.NoError(t, m.SaveSkillApproval(skillRec("a", "skill://one/SKILL.md", nil, nil)))
	require.NoError(t, m.SaveSkillApproval(skillRec("a:b", "skill://two/SKILL.md", nil, nil)))
	require.NoError(t, m.SaveSkillApproval(skillRec("c", "skill://three/SKILL.md", nil, nil)))

	got, err := m.ListSkillApprovals("a")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "skill://one/SKILL.md", got[0].SkillURI)

	all, err := m.ListSkillApprovals("")
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

func TestSkillFile_PutGetHasList(t *testing.T) {
	m := newPurgeTestManager(t)
	d := hexDigest('b')
	require.NoError(t, m.PutSkillFile("s", "sha256:"+d, []byte("abc")))
	require.NoError(t, m.PutSkillFile("a", hexDigest('c'), []byte("zz")))
	require.NoError(t, m.PutSkillFile("a:b", hexDigest('d'), []byte("yyy")))

	ok, err := m.HasSkillFile("s", d)
	require.NoError(t, err)
	assert.True(t, ok, "prefix and bare digest address the same row")
	ok, err = m.HasSkillFile("s", hexDigest('e'))
	require.NoError(t, err)
	assert.False(t, ok)

	b, err := m.GetSkillFile("s", d)
	require.NoError(t, err)
	assert.Equal(t, "abc", string(b))
	b[0] = 'X'
	again, err := m.GetSkillFile("s", d)
	require.NoError(t, err)
	assert.Equal(t, "abc", string(again), "returned slice is a copy")

	_, err = m.GetSkillFile("s", hexDigest('e'))
	assert.True(t, errors.Is(err, storage.ErrSkillFileNotFound))
	assert.Error(t, m.PutSkillFile("s", "nothex", []byte("x")))

	list, err := m.ListSkillFileDigests("a")
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{hexDigest('c'): 2}, list, "a must not see a:b")
}

func TestApplySkillRefresh(t *testing.T) {
	m := newPurgeTestManager(t)
	dA, dB, dC, dD := hexDigest('a'), hexDigest('b'), hexDigest('c'), hexDigest('d')

	// Existing: skill 1 current=A approved=B; skill 2 current=A (shared); blob C unreferenced.
	require.NoError(t, m.SaveSkillApproval(skillRec("s", "skill://one/SKILL.md", []storage.SkillFile{textFile("SKILL.md", dA)}, []storage.SkillFile{textFile("SKILL.md", dB)})))
	require.NoError(t, m.SaveSkillApproval(skillRec("s", "skill://two/SKILL.md", []storage.SkillFile{textFile("SKILL.md", dA)}, nil)))
	require.NoError(t, m.SaveSkillApproval(skillRec("s", "skill://three/SKILL.md", nil, nil)))
	for _, d := range []string{dA, dB, dC} {
		require.NoError(t, m.PutSkillFile("s", d, []byte("xyz")))
	}
	require.NoError(t, m.PutSkillFile("other", dC, []byte("xyz")))

	// Refresh: skill 1 current moves to D (A still shared with skill 2, B kept via Approved);
	// skill 3 gets a binary entry on C that must not count; skill 4 is new.
	up1 := skillRec("s", "skill://one/SKILL.md", []storage.SkillFile{textFile("SKILL.md", dD)}, []storage.SkillFile{textFile("SKILL.md", dB)})
	bin := storage.SkillFile{RelPath: "b.bin", Digest: "sha256:" + dC, Size: 3, Text: false, Stored: true}
	up3 := skillRec("s", "skill://three/SKILL.md", []storage.SkillFile{bin}, nil)
	up4 := skillRec("s", "skill://four/SKILL.md", nil, nil)
	removed, err := m.ApplySkillRefresh("s",
		[]*storage.SkillApprovalRecord{up1, up3, up4},
		[]string{"skill://two/SKILL.md", "skill://ghost/SKILL.md"},
		map[string][]byte{dD: []byte("new")})
	require.NoError(t, err)
	assert.Equal(t, 1, removed, "only C is unreferenced")

	two, err := m.GetSkillApproval("s", "skill://two/SKILL.md")
	require.NoError(t, err)
	assert.False(t, two.Listed)
	assert.Equal(t, storage.SkillStatusPending, two.Status, "other fields untouched")
	_, err = m.GetSkillApproval("s", "skill://ghost/SKILL.md")
	assert.True(t, errors.Is(err, storage.ErrSkillApprovalNotFound), "no record is created for an unknown URI")

	digests, err := m.ListSkillFileDigests("s")
	require.NoError(t, err)
	assert.Len(t, digests, 3)
	for _, d := range []string{dA, dB, dD} {
		assert.Contains(t, digests, d)
	}
	assert.NotContains(t, digests, dC, "binary entry does not keep a blob alive")
	ok, _ := m.HasSkillFile("other", dC)
	assert.True(t, ok, "another server's blob is untouched")
}

func TestApplySkillRefresh_RollsBackOnBadDigest(t *testing.T) {
	m := newPurgeTestManager(t)
	_, err := m.ApplySkillRefresh("s", []*storage.SkillApprovalRecord{skillRec("s", "skill://one/SKILL.md", nil, nil)}, nil, map[string][]byte{"bad": []byte("x")})
	require.Error(t, err)
	_, err = m.GetSkillApproval("s", "skill://one/SKILL.md")
	assert.True(t, errors.Is(err, storage.ErrSkillApprovalNotFound), "nothing written when the transaction fails")
}

func TestCollectUnreferencedSkillFiles(t *testing.T) {
	m := newPurgeTestManager(t)
	dA, dB := hexDigest('a'), hexDigest('b')
	require.NoError(t, m.SaveSkillApproval(skillRec("s", "skill://one/SKILL.md", []storage.SkillFile{textFile("SKILL.md", dA)}, nil)))
	require.NoError(t, m.PutSkillFile("s", dA, []byte("abcd")))
	require.NoError(t, m.PutSkillFile("s", dB, []byte("leftover")))

	removed, retained, err := m.CollectUnreferencedSkillFiles("s")
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.Equal(t, int64(4), retained)
}

func TestPurge_SkillBuckets(t *testing.T) {
	m := newPurgeTestManager(t)
	seedRows(t, m, 1, "acme", "acme_x")
	purgetest.Seed(t, m, "acme")
	purgetest.Seed(t, m, "acme_x")
	purgetest.AssertSeeded(t, m, "acme")
	c := purgetest.Counts(t, m, "acme")
	require.Equal(t, 1, c[purgetest.SkillApprovals])
	require.Equal(t, 1, c[purgetest.SkillFiles])
	before := purgetest.Counts(t, m, "acme_x")

	require.NoError(t, m.ApplyServerCommit(&storage.ServerCommitBatch{Generation: 2, Removes: []string{"acme"}}))

	purgetest.AssertPurged(t, m, "acme", "ApplyServerCommit.Removes")
	assert.Equal(t, before, purgetest.Counts(t, m, "acme_x"))
	counts, err := m.PurgeSetCounts("acme_x")
	require.NoError(t, err)
	assert.Equal(t, 1, counts[storage.SkillApprovalBucket])
	assert.Equal(t, 1, counts[storage.SkillFileBucket])
	counts, err = m.PurgeSetCounts("acme")
	require.NoError(t, err)
	assert.Zero(t, counts[storage.SkillApprovalBucket]+counts[storage.SkillFileBucket])
}

// A server named "a" must not purge the rows of "a:b" even though the key
// prefix "a:" matches them.
func TestPurge_SkillBucketsExactServer(t *testing.T) {
	m := newPurgeTestManager(t)
	require.NoError(t, m.SaveSkillApproval(skillRec("a", "skill://one/SKILL.md", nil, nil)))
	require.NoError(t, m.SaveSkillApproval(skillRec("a:b", "skill://two/SKILL.md", nil, nil)))
	require.NoError(t, m.PutSkillFile("a", hexDigest('a'), []byte("1")))
	require.NoError(t, m.PutSkillFile("a:b", hexDigest('b'), []byte("2")))
	seedRows(t, m, 1, "a")
	require.NoError(t, m.ApplyServerCommit(&storage.ServerCommitBatch{Generation: 2, Removes: []string{"a"}}))

	got, err := m.ListSkillApprovals("a:b")
	require.NoError(t, err)
	assert.Len(t, got, 1)
	files, err := m.ListSkillFileDigests("a:b")
	require.NoError(t, err)
	assert.Len(t, files, 1)
	gone, err := m.ListSkillApprovals("a")
	require.NoError(t, err)
	assert.Empty(t, gone)
}
