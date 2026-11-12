package controller

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	apierr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	wfv1 "github.com/argoproj/argo-workflows/v4/pkg/apis/workflow/v1alpha1"
	"github.com/argoproj/argo-workflows/v4/util/logging"
)

// aksWebhookStatus is the kube-apiserver response from
// https://github.com/argoproj/argo-workflows/issues/14220#issuecomment-2687749953,
// with the truncated webhook name and reason filled in.
const aksWebhookStatus = `{
  "code": 500,
  "metadata": {},
  "reason": "InternalError",
  "details": {"causes": [{"message": "failed calling webhook \"aks-webhook-admission-controller.azmk8s.io\": failed to call webhook: Post \"https://aks-webhook-admission-controller.kube-system.svc:443/validate?timeout=10s\": context deadline exceeded"}]},
  "status": "Failure",
  "message": "Internal error occurred: failed calling webhook \"aks-webhook-admission-controller.azmk8s.io\": failed to call webhook: Post \"https://aks-webhook-admission-controller.kube-system.svc:443/validate?timeout=10s\": context deadline exceeded"
}`

// aksWebhookPattern is the TRANSIENT_ERROR_PATTERN a user would configure on
// the workflow-controller to retry this error.
const aksWebhookPattern = `failed calling webhook "aks-webhook-admission-`

func aksWebhookErr(t *testing.T) error {
	t.Helper()
	var status metav1.Status
	require.NoError(t, json.Unmarshal([]byte(aksWebhookStatus), &status))
	return &apierr.StatusError{ErrStatus: status}
}

// runWithPodCreateErr operates hello-world once while pod creation fails with
// err, then once more after the apiserver recovers.
func runWithPodCreateErr(t *testing.T, err error) (first, second *wfv1.Workflow, createdPods int) {
	t.Helper()
	ctx := logging.TestContext(t.Context())
	cancel, controller := newController(ctx)
	defer cancel()

	failing := true
	controller.kubeclientset.(*fake.Clientset).PrependReactor("create", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		if failing {
			return true, nil, err
		}
		return false, nil, nil
	})

	woc := newWorkflowOperationCtx(ctx, wfv1.MustUnmarshalWorkflow(helloWorldWf), controller)
	woc.operate(ctx)
	first = woc.wf.DeepCopy()

	failing = false
	woc = newWorkflowOperationCtx(ctx, woc.wf, controller)
	woc.operate(ctx)

	pods, listErr := controller.kubeclientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	require.NoError(t, listErr)
	return first, woc.wf, len(pods.Items)
}

func podCount(t *testing.T, wf *wfv1.Workflow) int {
	t.Helper()
	n := 0
	for _, node := range wf.Status.Nodes {
		if node.Type == wfv1.NodeTypePod {
			n++
		}
	}
	return n
}

func TestTransientErrorPatternAKSWebhook(t *testing.T) {
	err := aksWebhookErr(t)
	require.True(t, apierr.IsInternalError(err))
	require.Equal(t, int32(500), err.(*apierr.StatusError).ErrStatus.Code)

	// retryStrategy.retryPolicy: OnTransientError classifies the failed node's
	// message, wrapped as an argo InternalError (not a kube StatusError).
	t.Run("OnTransientError retries a node failed by this error only with pattern", func(t *testing.T) {
		ctx := logging.TestContext(t.Context())
		node := &wfv1.NodeStatus{Phase: wfv1.NodeError, Message: err.Error()}
		policy := wfv1.RetryStrategy{RetryPolicy: wfv1.RetryPolicyOnTransientError}

		t.Setenv("TRANSIENT_ERROR_PATTERN", "")
		_, retryOnError, perr := retryPolicyAllows(ctx, node, policy)
		require.NoError(t, perr)
		assert.False(t, retryOnError)

		t.Setenv("TRANSIENT_ERROR_PATTERN", aksWebhookPattern)
		_, retryOnError, perr = retryPolicyAllows(ctx, node, policy)
		require.NoError(t, perr)
		assert.True(t, retryOnError)
	})

	t.Run("without pattern the workflow errors (issue 14220)", func(t *testing.T) {
		t.Setenv("TRANSIENT_ERROR_PATTERN", "")
		first, _, _ := runWithPodCreateErr(t, err)
		assert.Equal(t, wfv1.WorkflowError, first.Status.Phase)
		node := first.Status.Nodes.FindByDisplayName("hello-world")
		require.NotNil(t, node)
		assert.Equal(t, wfv1.NodeError, node.Phase)
		assert.Contains(t, node.Message, "failed calling webhook")
	})

	t.Run("with pattern the node waits and the pod is created on recovery", func(t *testing.T) {
		t.Setenv("TRANSIENT_ERROR_PATTERN", aksWebhookPattern)
		first, second, createdPods := runWithPodCreateErr(t, err)

		assert.Equal(t, wfv1.WorkflowRunning, first.Status.Phase)
		node := first.Status.Nodes.FindByDisplayName("hello-world")
		require.NotNil(t, node)
		assert.Equal(t, wfv1.NodePending, node.Phase)
		assert.Contains(t, node.Message, "failed calling webhook")

		assert.Equal(t, wfv1.WorkflowRunning, second.Status.Phase)
		node = second.Status.Nodes.FindByDisplayName("hello-world")
		require.NotNil(t, node)
		assert.Equal(t, wfv1.NodePending, node.Phase)
		assert.Equal(t, 1, podCount(t, second))
		assert.Equal(t, 1, createdPods, "pod is created once the apiserver recovers")
	})

	t.Run("pattern does not match a denial from the same webhook", func(t *testing.T) {
		t.Setenv("TRANSIENT_ERROR_PATTERN", aksWebhookPattern)
		denied := apierr.NewForbidden(apiv1.Resource("pods"), "hello-world",
			// what the apiserver returns for ErrWebhookRejection
			errFromString(`admission webhook "aks-webhook-admission-controller.azmk8s.io" denied the request: not allowed`))
		first, _, _ := runWithPodCreateErr(t, denied)
		assert.Equal(t, wfv1.WorkflowError, first.Status.Phase)
	})
}

type errFromString string

func (e errFromString) Error() string { return string(e) }
