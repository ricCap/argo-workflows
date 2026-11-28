package store

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/argoproj/argo-workflows/v4/util/logging"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	wfv1 "github.com/argoproj/argo-workflows/v4/pkg/apis/workflow/v1alpha1"
	"github.com/argoproj/argo-workflows/v4/util/instanceid"
)

func TestInitDB(t *testing.T) {
	conn, err := initDB()
	require.NoError(t, err)
	defer conn.Close()
	t.Run("TestTablesCreated", func(t *testing.T) {
		err = sqlitex.Execute(conn, `select name from sqlite_master where type='table'`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				name := stmt.ColumnText(0)
				assert.Contains(t, []string{workflowTableName, workflowLabelsTableName}, name)
				return nil
			},
		})
		require.NoError(t, err)
	})
	t.Run("TestForeignKeysEnabled", func(t *testing.T) {
		err = sqlitex.Execute(conn, `pragma foreign_keys`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				assert.Equal(t, "1", stmt.ColumnText(0))
				return nil
			},
		})
		require.NoError(t, err)
	})
	t.Run("TestIndexesCreated", func(t *testing.T) {
		var indexes []string
		err = sqlitex.Execute(conn, `select name from sqlite_master where type='index'`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				name := stmt.ColumnText(0)
				indexes = append(indexes, name)
				return nil
			},
		})
		require.NoError(t, err)
		assert.Contains(t, indexes, "idx_instanceid")
		assert.Contains(t, indexes, "idx_name_value")
	})
	t.Run("TestForeignKeysAdded", func(t *testing.T) {
		err = sqlitex.Execute(conn, `pragma foreign_key_list('argo_workflows_labels')`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				assert.Equal(t, "argo_workflows", stmt.ColumnText(2))
				assert.Equal(t, "uid", stmt.ColumnText(3))
				assert.Equal(t, "uid", stmt.ColumnText(4))
				assert.Equal(t, "CASCADE", stmt.ColumnText(6))
				return nil
			},
		})
		require.NoError(t, err)
	})
}

func TestStoreOperation(t *testing.T) {
	instanceIDSvc := instanceid.NewService("my-instanceid")
	conn, err := initDB()
	require.NoError(t, err)
	store := SQLiteStore{
		conn:            conn,
		instanceService: instanceIDSvc,
	}
	// Use a fixed reference time to avoid flaky boundary comparisons between
	// workflow timestamps and filter parameters.
	now := time.Now().UTC()
	t.Run("TestAddWorkflow", func(t *testing.T) {
		for i := range 10 {
			require.NoError(t, store.Add(generateWorkflow(now, i)))
		}
		ctx := logging.TestContext(t.Context())
		num, err := store.CountWorkflows(ctx, "argo", "", "", "", metav1.ListOptions{})
		require.NoError(t, err)
		assert.Equal(t, int64(10), num)
		// Labels are also added
		require.NoError(t, sqlitex.Execute(conn, `select count(*) from argo_workflows_labels`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				assert.Equal(t, 10*4, stmt.ColumnInt(0))
				return nil
			},
		}))
	})
	t.Run("TestUpdateWorkflow", func(t *testing.T) {
		wf := generateWorkflow(now, 0)
		wf.Labels["test-label-2"] = "value-2"
		require.NoError(t, store.Update(wf))
		// workflow is updated
		require.NoError(t, sqlitex.Execute(conn, `select workflow from argo_workflows where uid = 'uid-0'`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				w := stmt.ColumnText(0)
				require.NoError(t, json.Unmarshal([]byte(w), &wf))
				assert.Len(t, wf.Labels, 5)
				return nil
			},
		}))
		require.NoError(t, sqlitex.Execute(conn, `select count(*) from argo_workflows_labels where name = 'test-label-2' and value = 'value-2'`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				assert.Equal(t, 1, stmt.ColumnInt(0))
				return nil
			},
		}))
	})
	t.Run("TestDeleteWorkflow", func(t *testing.T) {
		wf := generateWorkflow(now, 0)
		require.NoError(t, store.Delete(wf))
		// workflow is deleted
		require.NoError(t, sqlitex.Execute(conn, `select count(*) from argo_workflows where uid = 'uid-0'`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				assert.Equal(t, 0, stmt.ColumnInt(0))
				return nil
			},
		}))
		// labels are also deleted
		require.NoError(t, sqlitex.Execute(conn, `select count(*) from argo_workflows_labels where uid = 'uid-0'`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				assert.Equal(t, 0, stmt.ColumnInt(0))
				return nil
			},
		}))
	})
	t.Run("TestListWorkflows", func(t *testing.T) {
		ctx := logging.TestContext(t.Context())
		wfList, err := store.ListWorkflows(ctx, "argo", "", "", "", metav1.ListOptions{Limit: 5})
		require.NoError(t, err)
		assert.Len(t, wfList.Items, 5)
	})
	t.Run("TestListWorkflows name", func(t *testing.T) {
		ctx := logging.TestContext(t.Context())
		wfList, err := store.ListWorkflows(ctx, "argo", "Exact", "", "", metav1.ListOptions{Limit: 5, FieldSelector: "metadata.name=flow"})
		require.NoError(t, err)
		assert.Empty(t, wfList.Items)

		wfList, err = store.ListWorkflows(ctx, "argo", "Exact", "", "", metav1.ListOptions{Limit: 5, FieldSelector: "metadata.name=workflow-1"})
		require.NoError(t, err)
		assert.Len(t, wfList.Items, 1)

		wfList, err = store.ListWorkflows(ctx, "argo", "", "", "", metav1.ListOptions{Limit: 5, FieldSelector: "metadata.name=workflow-1"})
		require.NoError(t, err)
		assert.Len(t, wfList.Items, 1)
	})
	t.Run("TestListWorkflows namePrefix", func(t *testing.T) {
		ctx := logging.TestContext(t.Context())
		wfList, err := store.ListWorkflows(ctx, "argo", "Prefix", "", "", metav1.ListOptions{Limit: 5, FieldSelector: "metadata.name=flow"})
		require.NoError(t, err)
		assert.Empty(t, wfList.Items)

		wfList, err = store.ListWorkflows(ctx, "argo", "Prefix", "", "", metav1.ListOptions{Limit: 5, FieldSelector: "metadata.name=workflow-"})
		require.NoError(t, err)
		assert.Len(t, wfList.Items, 5)

		wfList, err = store.ListWorkflows(ctx, "argo", "Prefix", "", "", metav1.ListOptions{Limit: 5, FieldSelector: "metadata.name=workflow-1"})
		require.NoError(t, err)
		assert.Len(t, wfList.Items, 1)
	})
	t.Run("TestListWorkflows namePattern", func(t *testing.T) {
		ctx := logging.TestContext(t.Context())
		wfList, err := store.ListWorkflows(ctx, "argo", "Contains", "", "", metav1.ListOptions{Limit: 5, FieldSelector: "metadata.name=non-existing-pattern"})
		require.NoError(t, err)
		assert.Empty(t, wfList.Items)

		wfList, err = store.ListWorkflows(ctx, "argo", "Contains", "", "", metav1.ListOptions{Limit: 5, FieldSelector: "metadata.name=flow"})
		require.NoError(t, err)
		assert.Len(t, wfList.Items, 5)

		wfList, err = store.ListWorkflows(ctx, "argo", "Contains", "", "", metav1.ListOptions{Limit: 5, FieldSelector: "metadata.name=workflow-1"})
		require.NoError(t, err)
		assert.Len(t, wfList.Items, 1)
	})
	t.Run("TestListWorkflows finishedBefore", func(t *testing.T) {
		ctx := logging.TestContext(t.Context())
		// Finished before today
		wfList, err := store.ListWorkflows(ctx, "argo", "", "", now.Format(time.RFC3339), metav1.ListOptions{})
		require.NoError(t, err)
		assert.Len(t, wfList.Items, 9)

		// Finished before 1 day ago
		wfList, err = store.ListWorkflows(ctx, "argo", "", "", now.Add(-24*time.Hour).Format(time.RFC3339), metav1.ListOptions{})
		require.NoError(t, err)
		assert.Len(t, wfList.Items, 8)

		// Finished before 5 days ago
		wfList, err = store.ListWorkflows(ctx, "argo", "", "", now.Add(-5*24*time.Hour).Format(time.RFC3339), metav1.ListOptions{})
		require.NoError(t, err)
		assert.Len(t, wfList.Items, 4)

		// Finished before 10 days ago
		wfList, err = store.ListWorkflows(ctx, "argo", "", "", now.Add(-24*10*time.Hour).Format(time.RFC3339), metav1.ListOptions{})
		require.NoError(t, err)
		assert.Empty(t, wfList.Items)
	})
	t.Run("TestListWorkflows createdAfter", func(t *testing.T) {
		ctx := logging.TestContext(t.Context())
		// Created after today
		wfList, err := store.ListWorkflows(ctx, "argo", "", now.Format(time.RFC3339), "", metav1.ListOptions{})
		require.NoError(t, err)
		assert.Empty(t, wfList.Items)

		// Created after 1 day ago
		wfList, err = store.ListWorkflows(ctx, "argo", "", now.Add(-24*time.Hour).Format(time.RFC3339), "", metav1.ListOptions{})
		require.NoError(t, err)
		assert.Len(t, wfList.Items, 1)

		// Created after 3 days ago
		wfList, err = store.ListWorkflows(ctx, "argo", "", now.Add(-3*24*time.Hour).Format(time.RFC3339), "", metav1.ListOptions{})
		require.NoError(t, err)
		assert.Len(t, wfList.Items, 3)

		// Created after 10 days ago
		wfList, err = store.ListWorkflows(ctx, "argo", "", now.Add(-10*24*time.Hour).Format(time.RFC3339), "", metav1.ListOptions{})
		require.NoError(t, err)
		assert.Len(t, wfList.Items, 9)
	})
	t.Run("TestCountWorkflows", func(t *testing.T) {
		ctx := logging.TestContext(t.Context())
		num, err := store.CountWorkflows(ctx, "argo", "", "", "", metav1.ListOptions{})
		require.NoError(t, err)
		assert.Equal(t, int64(9), num)
	})
}

func TestStoreLastSyncResourceVersion(t *testing.T) {
	store, err := NewSQLiteStore(instanceid.NewService("my-instanceid"))
	require.NoError(t, err)
	assert.Empty(t, store.LastStoreSyncResourceVersion(), "nothing synced yet")

	withRV := func(uid int, rv string) *wfv1.Workflow {
		wf := generateWorkflow(time.Now(), uid)
		wf.ResourceVersion = rv
		return wf
	}

	require.NoError(t, store.Replace([]any{withRV(0, "5")}, "10"))
	assert.Equal(t, "10", store.LastStoreSyncResourceVersion(), "Replace records the list resource version")

	require.NoError(t, store.Add(withRV(1, "11")))
	assert.Equal(t, "11", store.LastStoreSyncResourceVersion())

	require.NoError(t, store.Update(withRV(1, "12")))
	assert.Equal(t, "12", store.LastStoreSyncResourceVersion())

	require.NoError(t, store.Delete(withRV(1, "13")))
	assert.Equal(t, "13", store.LastStoreSyncResourceVersion())

	store.Bookmark("20")
	assert.Equal(t, "20", store.LastStoreSyncResourceVersion())

	// Failed writes must not move the resource version.
	require.NoError(t, sqlitex.ExecuteScript(store.conn, "drop table "+workflowLabelsTableName+"; drop table "+workflowTableName+";", nil))
	require.Error(t, store.Add(withRV(2, "21")))
	require.Error(t, store.Update(withRV(2, "22")))
	require.Error(t, store.Delete(withRV(0, "23")))
	require.Error(t, store.Replace([]any{withRV(2, "24")}, "25"))
	assert.Equal(t, "20", store.LastStoreSyncResourceVersion(), "a failed write leaves the resource version unchanged")
}

// A write whose COMMIT fails returns the error, keeps the previous resource
// version and leaves no rows behind.
func TestStoreCommitFailure(t *testing.T) {
	store, err := NewSQLiteStore(instanceid.NewService("my-instanceid"))
	require.NoError(t, err)
	store.Bookmark("1")
	// The trigger's orphan row breaks a deferred foreign key: the upsert
	// succeeds and only COMMIT fails.
	require.NoError(t, sqlitex.ExecuteScript(store.conn, `
create table commit_guard (uid varchar(128) references argo_workflows (uid) deferrable initially deferred);
create trigger orphan_guard after insert on argo_workflows begin
  insert into commit_guard (uid) values ('missing');
end;`, nil))
	countWorkflows := func() int {
		n := -1
		require.NoError(t, sqlitex.ExecuteTransient(store.conn, "select count(*) from "+workflowTableName, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error { n = stmt.ColumnInt(0); return nil },
		}))
		return n
	}
	wf := generateWorkflow(time.Now(), 0)
	wf.ResourceVersion = "2"

	require.Error(t, store.Add(wf))
	assert.Equal(t, "1", store.LastStoreSyncResourceVersion())
	assert.Equal(t, 0, countWorkflows(), "the failed transaction is rolled back")

	require.NoError(t, sqlitex.ExecuteTransient(store.conn, "drop trigger orphan_guard", nil))
	require.NoError(t, store.Add(wf))
	assert.Equal(t, "2", store.LastStoreSyncResourceVersion())
	assert.Equal(t, 1, countWorkflows())
}

func generateWorkflow(now time.Time, uid int) *wfv1.Workflow {
	ts := now.Add(-24 * time.Duration(uid) * time.Hour)
	return &wfv1.Workflow{ObjectMeta: metav1.ObjectMeta{
		UID:               types.UID(fmt.Sprintf("uid-%d", uid)),
		Name:              fmt.Sprintf("workflow-%d", uid),
		Namespace:         "argo",
		CreationTimestamp: metav1.Time{Time: ts},
		Labels: map[string]string{
			"workflows.argoproj.io/completed":             "true",
			"workflows.argoproj.io/phase":                 "Succeeded",
			"workflows.argoproj.io/controller-instanceid": "my-instanceid",
			"test-label": fmt.Sprintf("label-%d", uid),
		},
	}, Status: wfv1.WorkflowStatus{FinishedAt: metav1.NewTime(ts)}}
}
