package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	batchv1 "k8s.io/api/batch/v1"

	corev1 "k8s.io/api/core/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	apimeta "k8s.io/apimachinery/pkg/api/meta"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	memorycache "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

// k8sClients holds typed and dynamic k8s API clients for one cluster.
type k8sClients struct {
	config    *rest.Config
	clientset kubernetes.Interface
	dynamic   dynamic.Interface
	mapper    *restmapper.DeferredDiscoveryRESTMapper
}

func newK8sClients(r hosts.Record) (*k8sClients, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kc := r.Meta["kubeconfig"]; kc != "" {
		loadingRules.ExplicitPath = kc
	}
	overrides := &clientcmd.ConfigOverrides{}
	if kctx := r.Meta["kube_context"]; kctx != "" {
		overrides.CurrentContext = kctx
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("k8s config from host %q: %w", r.Name, err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s clientset: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s dynamic client: %w", err)
	}
	disc := memorycache.NewMemCacheClient(clientset.Discovery())
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(disc)
	return &k8sClients{config: cfg, clientset: clientset, dynamic: dyn, mapper: mapper}, nil
}

// mappingFor returns the REST mapping for a resource string (singular or plural, e.g. "deployment" or "deployments").
func (c *k8sClients) mappingFor(resource string) (*apimeta.RESTMapping, error) {
	gvr, err := c.mapper.ResourceFor(schema.GroupVersionResource{Resource: strings.ToLower(resource)})
	if err != nil {
		return nil, fmt.Errorf("resource %q not found in cluster: %w", resource, err)
	}
	gvk, err := c.mapper.KindFor(gvr)
	if err != nil {
		return nil, fmt.Errorf("kind for %q: %w", resource, err)
	}
	mapping, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("rest mapping for %q: %w", resource, err)
	}
	return mapping, nil
}

// dynamicClient returns the dynamic resource interface, namespaced when applicable.
func (c *k8sClients) dynamicClient(mapping *apimeta.RESTMapping, ns string) dynamic.ResourceInterface {
	ri := c.dynamic.Resource(mapping.Resource)
	if mapping.Scope.Name() == apimeta.RESTScopeNameNamespace {
		return ri.Namespace(ns)
	}
	return ri
}

// k8sNamespace resolves the effective namespace for a step: step field → host meta → "default".
func k8sNamespace(step *cuetry.RecipeStepK8s, r hosts.Record) string {
	if ns := strings.TrimSpace(step.Namespace); ns != "" {
		return ns
	}
	if ns := r.Meta["namespace"]; ns != "" {
		return ns
	}
	return "default"
}

// parseK8sResource splits "kind/name" into (kind, name). Name is empty for list operations.
func parseK8sResource(s string) (kind, name string) {
	parts := strings.SplitN(strings.TrimSpace(s), "/", 2)
	kind = parts[0]
	if len(parts) == 2 {
		name = parts[1]
	}
	return
}

// StreamCueStepK8s ...
func StreamCueStepK8s(
	ctx context.Context,
	run *CueRun,
	stepIdx int,
	step cuetry.Step,
	targets []hosts.Record,
	ch chan<- HostExecResult,
	retryCfg cuetry.RecipeStepRetry,
	attemptMax *atomic.Int32,
) error {
	if _, ok := step.(*cuetry.K8sStep); !ok {
		return fmt.Errorf("internal: k8s step missing k8s field")
	}
	maxConc := RecipeHostMaxConc(step, run.Params.Recipe.Defaults)
	if maxConc <= 0 {
		maxConc = 8
	}
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := runK8sActionOnHost(ctx, run, stepIdx, step, target, retryCfg, attemptMax)
			ch <- res
		}()
	}
	wg.Wait()
	return nil
}

func runK8sActionOnHost(
	ctx context.Context,
	run *CueRun,
	stepIdx int,
	step cuetry.Step,
	target hosts.Record,
	retryCfg cuetry.RecipeStepRetry,
	attemptMax *atomic.Int32,
) HostExecResult {
	res := HostExecResult{
		Name:     target.Name,
		IP:       target.PrimaryIP,
		Provider: target.Provider,
	}
	ks, _ := step.(*cuetry.K8sStep)
	if ks == nil || ks.K8s == nil {
		res.ErrMsg = "internal: k8s step missing k8s field"
		return res
	}
	k := ks.K8s
	ns := k8sNamespace(k, target)

	clients, err := newK8sClients(target)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}

	// Resolve env once for ${VAR} expansion in manifest and other action fields.
	stepEnv, err := cuetry.EffectiveEnvForRunEx(ctx, run.Params.Execute, run.Params.SecretResolver, step.Base(), run.Params.Recipe.Defaults, run.Params.CLIEnv, &target, CueEnvRunOpts(&run.Params.Recipe, run.OutputStore, run.OutputCapture, KvReaderFromCoordinator(run.RecipeKV), !run.Params.Execute))
	if err != nil {
		res.ErrMsg = fmt.Errorf("k8s step env: %w", err).Error()
		return res
	}

	var output string
	outcome := RunHostExecWithRetry(ctx, retryCfg, func() HostExecResult {
		inner := HostExecResult{Name: target.Name, IP: target.PrimaryIP, Provider: target.Provider}
		var actionErr error
		switch {
		case k.Apply != nil:
			output, actionErr = execK8sApply(ctx, clients, ns, k.Apply, stepEnv)
		case k.Delete != nil:
			output, actionErr = execK8sDelete(ctx, clients, ns, k.Delete)
		case k.Scale != nil:
			output, actionErr = execK8sScale(ctx, clients, ns, k.Scale)
		case k.RolloutRestart != nil:
			output, actionErr = execK8sRolloutRestart(ctx, clients, ns, k.RolloutRestart)
		case k.Wait != nil:
			output, actionErr = execK8sWait(ctx, clients, ns, k.Wait)
		case k.Get != nil:
			output, actionErr = execK8sGet(ctx, clients, ns, k.Get)
		case k.Exec != nil:
			output, actionErr = execK8sExec(ctx, clients, ns, k.Exec)
		case k.CreateJob != nil:
			output, actionErr = execK8sCreateJob(ctx, clients, ns, k.CreateJob)
		default:
			actionErr = fmt.Errorf("no action set on k8s step")
		}
		inner.Output = output
		if actionErr != nil {
			inner.ErrMsg = actionErr.Error()
			return inner
		}
		inner.Success = true
		return inner
	})
	RecordMaxAttempts(attemptMax, outcome.Attempts)
	res = outcome.Result

	if res.Success && strings.TrimSpace(k.Output) != "" && run.OutputCapture != nil {
		run.OutputCapture.Set(strings.TrimSpace(k.Output), res.Output)
	}

	RunCueStepHooks(ctx, run, stepIdx, cuetry.KindK8s, step, target, &res, false)
	return res
}

func execK8sApply(ctx context.Context, c *k8sClients, ns string, a *cuetry.K8sApply, env map[string]string) (string, error) {
	manifest, err := cuetry.ExpandRecipeVars(a.Manifest, env, false)
	if err != nil {
		return "", fmt.Errorf("expand manifest vars: %w", err)
	}
	jsonData, err := utilyaml.ToJSON([]byte(manifest))
	if err != nil {
		return "", fmt.Errorf("parse manifest: %w", err)
	}
	var objMap map[string]interface{}
	if err := json.Unmarshal(jsonData, &objMap); err != nil {
		return "", fmt.Errorf("decode manifest: %w", err)
	}
	obj := &unstructured.Unstructured{Object: objMap}
	apiVersion := obj.GetAPIVersion()
	kind := obj.GetKind()
	if apiVersion == "" || kind == "" {
		return "", fmt.Errorf("manifest missing apiVersion or kind")
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return "", fmt.Errorf("parse apiVersion %q: %w", apiVersion, err)
	}
	mapping, err := c.mapper.RESTMapping(schema.GroupKind{Group: gv.Group, Kind: kind}, gv.Version)
	if err != nil {
		return "", fmt.Errorf("rest mapping for %s/%s: %w", apiVersion, kind, err)
	}
	ri := c.dynamicClient(mapping, ns)
	opts := metav1.ApplyOptions{FieldManager: "honey", Force: a.Force}
	result, err := ri.Apply(ctx, obj.GetName(), obj, opts)
	if err != nil {
		return "", fmt.Errorf("apply %s/%s: %w", kind, obj.GetName(), err)
	}
	return fmt.Sprintf("applied %s/%s", result.GetKind(), result.GetName()), nil
}

func execK8sDelete(ctx context.Context, c *k8sClients, ns string, d *cuetry.K8sDelete) (string, error) {
	kind, name := parseK8sResource(d.Resource)
	if name == "" {
		return "", fmt.Errorf("k8s.delete.resource must be kind/name, got %q", d.Resource)
	}
	mapping, err := c.mappingFor(kind)
	if err != nil {
		return "", err
	}
	ri := c.dynamicClient(mapping, ns)
	policy := metav1.DeletePropagationForeground
	if d.Wait {
		policy = metav1.DeletePropagationForeground
	}
	if err := ri.Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &policy}); err != nil && !k8serrors.IsNotFound(err) {
		return "", fmt.Errorf("delete %s/%s: %w", kind, name, err)
	}
	if d.Wait {
		deadline := time.Now().Add(5 * time.Minute)
		for time.Now().Before(deadline) {
			_, err := ri.Get(ctx, name, metav1.GetOptions{})
			if k8serrors.IsNotFound(err) {
				break
			}
			if err != nil {
				return "", fmt.Errorf("wait delete %s/%s: %w", kind, name, err)
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
	return fmt.Sprintf("deleted %s/%s", kind, name), nil
}

func execK8sScale(ctx context.Context, c *k8sClients, ns string, s *cuetry.K8sScale) (string, error) {
	kind, name := parseK8sResource(s.Resource)
	if name == "" {
		return "", fmt.Errorf("k8s.scale.resource must be kind/name, got %q", s.Resource)
	}
	replicas := s.Replicas
	switch strings.ToLower(kind) {
	case "deployment", "deployments":
		sc, err := c.clientset.AppsV1().Deployments(ns).GetScale(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("get scale deployment/%s: %w", name, err)
		}
		sc.Spec.Replicas = replicas
		if _, err := c.clientset.AppsV1().Deployments(ns).UpdateScale(ctx, name, sc, metav1.UpdateOptions{}); err != nil {
			return "", fmt.Errorf("update scale deployment/%s: %w", name, err)
		}
	case "statefulset", "statefulsets":
		sc, err := c.clientset.AppsV1().StatefulSets(ns).GetScale(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("get scale statefulset/%s: %w", name, err)
		}
		sc.Spec.Replicas = replicas
		if _, err := c.clientset.AppsV1().StatefulSets(ns).UpdateScale(ctx, name, sc, metav1.UpdateOptions{}); err != nil {
			return "", fmt.Errorf("update scale statefulset/%s: %w", name, err)
		}
	case "replicaset", "replicasets":
		sc, err := c.clientset.AppsV1().ReplicaSets(ns).GetScale(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("get scale replicaset/%s: %w", name, err)
		}
		sc.Spec.Replicas = replicas
		if _, err := c.clientset.AppsV1().ReplicaSets(ns).UpdateScale(ctx, name, sc, metav1.UpdateOptions{}); err != nil {
			return "", fmt.Errorf("update scale replicaset/%s: %w", name, err)
		}
	default:
		return "", fmt.Errorf("scale not supported for %q (supported: deployment, statefulset, replicaset)", kind)
	}
	return fmt.Sprintf("scaled %s/%s to %d", kind, name, s.Replicas), nil
}

func execK8sRolloutRestart(ctx context.Context, c *k8sClients, ns string, r *cuetry.K8sRolloutRestart) (string, error) {
	kind, name := parseK8sResource(r.Resource)
	if name == "" {
		return "", fmt.Errorf("k8s.rollout_restart.resource must be kind/name, got %q", r.Resource)
	}
	restartedAt := time.Now().UTC().Format(time.RFC3339)
	patch := []byte(fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`,
		restartedAt,
	))
	var patchErr error
	switch strings.ToLower(kind) {
	case "deployment", "deployments":
		_, patchErr = c.clientset.AppsV1().Deployments(ns).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	case "statefulset", "statefulsets":
		_, patchErr = c.clientset.AppsV1().StatefulSets(ns).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	case "daemonset", "daemonsets":
		_, patchErr = c.clientset.AppsV1().DaemonSets(ns).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	default:
		return "", fmt.Errorf("rollout_restart not supported for %q (supported: deployment, statefulset, daemonset)", kind)
	}
	if patchErr != nil {
		return "", fmt.Errorf("patch %s/%s: %w", kind, name, patchErr)
	}
	if !r.Wait {
		return fmt.Sprintf("rollout restart triggered for %s/%s", kind, name), nil
	}
	// Wait for rollout to complete by polling observedGeneration + availableReplicas.
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		done, err := rolloutComplete(ctx, c, ns, kind, name)
		if err != nil {
			return "", fmt.Errorf("wait rollout %s/%s: %w", kind, name, err)
		}
		if done {
			return fmt.Sprintf("rollout restart complete for %s/%s", kind, name), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return "", fmt.Errorf("rollout restart timed out for %s/%s", kind, name)
}

func rolloutComplete(ctx context.Context, c *k8sClients, ns, kind, name string) (bool, error) {
	switch strings.ToLower(kind) {
	case "deployment", "deployments":
		dep, err := c.clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		desired := dep.Spec.Replicas
		if desired == nil {
			return dep.Status.ObservedGeneration >= dep.Generation && dep.Status.UnavailableReplicas == 0, nil
		}
		return dep.Status.ObservedGeneration >= dep.Generation &&
			dep.Status.UpdatedReplicas == *desired &&
			dep.Status.AvailableReplicas == *desired &&
			dep.Status.UnavailableReplicas == 0, nil
	case "statefulset", "statefulsets":
		ss, err := c.clientset.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		desired := ss.Spec.Replicas
		if desired == nil {
			return ss.Status.ObservedGeneration >= ss.Generation, nil
		}
		return ss.Status.ObservedGeneration >= ss.Generation &&
			ss.Status.UpdatedReplicas == *desired &&
			ss.Status.ReadyReplicas == *desired, nil
	case "daemonset", "daemonsets":
		ds, err := c.clientset.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return ds.Status.ObservedGeneration >= ds.Generation &&
			ds.Status.NumberUnavailable == 0 &&
			ds.Status.UpdatedNumberScheduled == ds.Status.DesiredNumberScheduled, nil
	}
	return false, fmt.Errorf("rollout check not supported for %q", kind)
}

func execK8sWait(ctx context.Context, c *k8sClients, ns string, w *cuetry.K8sWait) (string, error) {
	kind, name := parseK8sResource(w.Resource)
	if name == "" {
		return "", fmt.Errorf("k8s.wait.resource must be kind/name, got %q", w.Resource)
	}
	timeout := 5 * time.Minute
	if t := strings.TrimSpace(w.Timeout); t != "" {
		d, err := time.ParseDuration(t)
		if err != nil {
			return "", fmt.Errorf("parse timeout %q: %w", t, err)
		}
		timeout = d
	}
	mapping, err := c.mappingFor(kind)
	if err != nil {
		return "", err
	}
	ri := c.dynamicClient(mapping, ns)
	// Parse "condition=Available" or "condition=available"
	var forKey, condName string
	{
		parts := strings.SplitN(strings.TrimSpace(w.For), "=", 2)
		if len(parts) == 2 {
			forKey = strings.ToLower(parts[0])
			condName = parts[1]
		} else {
			forKey = strings.ToLower(w.For)
		}
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		obj, err := ri.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("get %s/%s: %w", kind, name, err)
		}
		if checkK8sCondition(obj, forKey, condName) {
			return fmt.Sprintf("%s/%s condition %q met", kind, name, w.For), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return "", fmt.Errorf("wait timed out for %s/%s (for: %s)", kind, name, w.For)
}

// checkK8sCondition checks if an unstructured object meets the given condition.
func checkK8sCondition(obj *unstructured.Unstructured, forKey, condName string) bool {
	if forKey != "condition" {
		return false
	}
	conditions, found, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if !found {
		return false
	}
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		t, _, _ := unstructured.NestedString(cond, "type")
		s, _, _ := unstructured.NestedString(cond, "status")
		if strings.EqualFold(t, condName) && strings.EqualFold(s, "true") {
			return true
		}
	}
	return false
}

func execK8sGet(ctx context.Context, c *k8sClients, ns string, g *cuetry.K8sGet) (string, error) {
	kind, name := parseK8sResource(g.Resource)
	mapping, err := c.mappingFor(kind)
	if err != nil {
		return "", err
	}
	ri := c.dynamicClient(mapping, ns)
	format := strings.ToLower(strings.TrimSpace(g.Format))
	if format == "" {
		format = "json"
	}
	var result interface{}
	if name != "" {
		obj, err := ri.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("get %s/%s: %w", kind, name, err)
		}
		result = obj
	} else {
		list, err := ri.List(ctx, metav1.ListOptions{LabelSelector: strings.TrimSpace(g.LabelSelector)})
		if err != nil {
			return "", fmt.Errorf("list %s: %w", kind, err)
		}
		result = list
	}
	if format == "name" {
		if obj, ok := result.(*unstructured.Unstructured); ok {
			return fmt.Sprintf("%s/%s", strings.ToLower(obj.GetKind()), obj.GetName()), nil
		}
		if list, ok := result.(*unstructured.UnstructuredList); ok {
			var names []string
			for _, item := range list.Items {
				names = append(names, fmt.Sprintf("%s/%s", strings.ToLower(item.GetKind()), item.GetName()))
			}
			return strings.Join(names, "\n"), nil
		}
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(data), nil
}

func execK8sExec(ctx context.Context, c *k8sClients, ns string, e *cuetry.K8sExec) (string, error) {
	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(strings.TrimSpace(e.Pod)).
		Namespace(ns).
		SubResource("exec")
	opts := &corev1.PodExecOptions{
		Command: e.Command,
		Stdout:  true,
		Stderr:  true,
		TTY:     e.TTY,
	}
	if e.Container != "" {
		opts.Container = e.Container
	}
	req.VersionedParams(opts, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(c.config, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("create executor for pod/%s: %w", e.Pod, err)
	}
	var stdout, stderr bytes.Buffer
	streamErr := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    e.TTY,
	})
	output := strings.TrimSpace(stdout.String())
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += strings.TrimSpace(stderr.String())
	}
	if streamErr != nil {
		return output, fmt.Errorf("exec pod/%s: %w", e.Pod, streamErr)
	}
	return output, nil
}

func execK8sCreateJob(ctx context.Context, c *k8sClients, ns string, j *cuetry.K8sCreateJob) (string, error) {
	restartPolicy := corev1.RestartPolicyNever
	if strings.EqualFold(j.RestartPolicy, "OnFailure") {
		restartPolicy = corev1.RestartPolicyOnFailure
	}
	envVars := make([]corev1.EnvVar, 0, len(j.Env))
	for k, v := range j.Env {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      j.Name,
			Namespace: ns,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: restartPolicy,
					Containers: []corev1.Container{
						{
							Name:    "job",
							Image:   j.Image,
							Command: j.Command,
							Args:    j.Args,
							Env:     envVars,
						},
					},
				},
			},
		},
	}
	if j.TTLSeconds > 0 {
		ttl := j.TTLSeconds
		job.Spec.TTLSecondsAfterFinished = &ttl
	}
	created, err := c.clientset.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create job/%s: %w", j.Name, err)
	}
	if !j.Wait {
		return fmt.Sprintf("job/%s created", created.Name), nil
	}
	// Poll until job succeeds or fails.
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		current, err := c.clientset.BatchV1().Jobs(ns).Get(ctx, j.Name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("get job/%s: %w", j.Name, err)
		}
		for _, cond := range current.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				logs := collectJobLogs(ctx, c, ns, j.Name)
				return fmt.Sprintf("job/%s complete\n%s", j.Name, logs), nil
			}
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				logs := collectJobLogs(ctx, c, ns, j.Name)
				return logs, fmt.Errorf("job/%s failed: %s", j.Name, cond.Message)
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return "", fmt.Errorf("job/%s timed out after 30 minutes", j.Name)
}

// collectJobLogs fetches logs from the first pod of a job. Returns empty string on error.
func collectJobLogs(ctx context.Context, c *k8sClients, ns, jobName string) string {
	pods, err := c.clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", jobName),
	})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}
	pod := pods.Items[0]
	req := c.clientset.CoreV1().Pods(ns).GetLogs(pod.Name, &corev1.PodLogOptions{})
	rc, err := req.Stream(ctx)
	if err != nil {
		return ""
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
