package runtime

import (
	"fmt"
	"sort"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 117 FR-032a (#1610): the absent-tool removal of a discovery pass is
// reconciled against the server's approval records as well as against the
// index documents the previous pass left.
//
// The index-derived set alone cannot recover from a partial failure: a pass
// whose index delete of x succeeded and whose record delete failed leaves a
// record that no later pass revisits, because x is no longer in the index.
// The record then outlives the tool, and a reappearing x with the old
// definition would be approved again without review. Deriving the extra set
// from storage also survives a restart: the first complete capture after it
// removes a record left behind before.
//
// Retention is unchanged: pending, changed and user-disabled records of
// absent tools are kept (the operator's decision and the review history),
// and so is the pre-105 collapsed alias of a namespaced tool still served.

// absentApprovedRemovable reports whether rec is an approved, enabled record
// of a tool the capture live does not list. It is evaluated again against the
// stored value inside the delete transaction (DeleteToolApprovalIf), so an
// operator decision taken after the listing survives.
func absentApprovedRemovable(rec *storage.ToolApprovalRecord, live map[string]*config.ToolMetadata) bool {
	if rec == nil {
		return false
	}
	if _, served := live[rec.ToolName]; served {
		return false
	}
	return rec.Status == storage.ToolApprovalStatusApproved && !rec.Disabled
}

// absentApprovedRecords returns, sorted, the names of serverName's approved
// and enabled records whose tool the capture (live) does not list and that
// the index diff does not already remove (indexed), minus retained pre-105
// aliases. Caller holds the server's approval lock.
func (r *Runtime) absentApprovedRecords(serverName string, live, indexed map[string]*config.ToolMetadata, alias map[string]bool) ([]string, error) {
	if r.storageManager == nil {
		return nil, nil
	}
	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		return nil, fmt.Errorf("%w: list approval records of %s for absent-tool removal: %v", errCaptureIncomplete, serverName, err)
	}
	var names []string
	for _, rec := range records {
		if _, inIndex := indexed[rec.ToolName]; inIndex {
			continue
		}
		if alias[rec.ToolName] || !absentApprovedRemovable(rec, live) {
			continue
		}
		names = append(names, rec.ToolName)
	}
	sort.Strings(names)
	return names, nil
}

// removeAbsentTool deletes toolName's index document and tool hash and, when
// deleteRecord is set, its approval record (only while cond holds for the
// stored value; nil deletes unconditionally). The index and hash deletes both
// run; the record is deleted only after both succeeded, so a removal that
// failed part-way keeps its record as the next pass's retry source (the
// record-derived workset). Every delete is idempotent, so the next pass
// converges; the first error is returned.
func (r *Runtime) removeAbsentTool(serverName, toolName string, deleteRecord bool, cond func(*storage.ToolApprovalRecord) bool) error {
	var first error
	note := func(err error) {
		if err != nil {
			r.noteFailedTool(serverName, toolName)
			if first == nil {
				first = err
			}
		}
	}
	fault := func(op string) error {
		if f := r.testHooks.toolApprovalWriteFault; f != nil {
			return f(serverName, toolName, op)
		}
		return nil
	}

	if r.indexManager != nil {
		err := fault("delete-index")
		if err == nil {
			err = r.indexManager.DeleteTool(serverName, toolName)
		}
		if err != nil {
			note(fmt.Errorf("delete index entry %s:%s: %w", serverName, toolName, err))
		}
	}
	if r.storageManager == nil {
		return first
	}
	fullToolName := fmt.Sprintf("%s:%s", serverName, toolName)
	err := fault("delete-hash")
	if err == nil {
		err = r.storageManager.DeleteToolHash(fullToolName)
	}
	if err != nil {
		note(fmt.Errorf("delete tool hash %s: %w", fullToolName, err))
	}
	if !deleteRecord || first != nil {
		return first
	}
	err = fault("delete-record")
	if err == nil {
		if cond == nil {
			err = r.storageManager.DeleteToolApproval(serverName, toolName)
		} else {
			_, err = r.storageManager.DeleteToolApprovalIf(serverName, toolName, cond)
		}
	}
	if err != nil {
		note(fmt.Errorf("delete approval record %s: %w", fullToolName, err))
	}
	return first
}

// indexedToolsOf is the discovery pass's read of the server's indexed tools,
// with the FR-018 test fault seam in front of it.
func (r *Runtime) indexedToolsOf(serverName string) ([]*config.ToolMetadata, error) {
	if f := r.testHooks.toolIndexReadFault; f != nil {
		if err := f(serverName); err != nil {
			return nil, err
		}
	}
	return r.indexManager.GetToolsByServer(serverName)
}

// batchIndexTools is the discovery pass's index write, with the FR-018 test
// fault seam in front of it.
func (r *Runtime) batchIndexTools(serverName string, tools []*config.ToolMetadata) error {
	if f := r.testHooks.toolIndexWriteFault; f != nil {
		if err := f(serverName); err != nil {
			return err
		}
	}
	return r.indexManager.BatchIndexTools(tools)
}
