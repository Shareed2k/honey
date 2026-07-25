import type { IDockviewPanelProps } from 'dockview-react';
import type { ParsedRecipe } from '../../../api/types/recipes';
import type { DocState } from '../types';
import { useWorkspaceStore } from '../store';
import { StepRun } from '../../../RecipesTab/StepRun';
import { WizardProvider } from '../../../RecipesTab/WizardContext';
import { buildRecipeFromFlow, collectAncestorNodeIDs, recipeNameFromFilename } from '../../useRecipeGraph';
import { useHostSelection } from '../../../contexts/HostSelectionContext';

// Forward BFS from `targetId` over doc.edges — the descendant counterpart to
// collectAncestorNodeIDs (useRecipeGraph.ts), ported from the old (pre-
// dockview) useRecipeStudioEngine.ts's buildDownstreamRecipe traversal (see
// webui/src/RecipeStudio/useRecipeStudioEngine.ts, git history). Used for
// "Resume from here": the target step + everything reachable by following
// edges forward (its descendants), instead of collectAncestorNodeIDs' walk
// backward along incoming edges.
function collectDescendantNodeIDs(edges: { source: string; target: string }[], targetId: string): Set<string> {
  const visited = new Set<string>([targetId]);
  const visit = (id: string) => {
    for (const edge of edges) {
      if (edge.source === id && !visited.has(edge.target)) {
        visited.add(edge.target);
        visit(edge.target);
      }
    }
  };
  visit(targetId);
  return visited;
}

// Builds the recipe JSON to execute for the current run: with `stepId` set,
// either the step + its ancestors ("Run Step" / mode 'upstream') or the step
// + its descendants ("Resume from here" / mode 'downstream'); or the whole
// doc when stepId is null ("Run recipe") — mirrors the old (pre-dockview)
// StudioWorkspace's buildSubgraphRecipe/buildDownstreamRecipe, adapted to
// buildRecipeFromFlow's real signature (`{ name, nodes, edges, stepData }` —
// it has no `targetStepId` param).
function buildRunRecipe(doc: DocState, stepId: string | null, mode: 'upstream' | 'downstream') {
  let nodes = doc.nodes;
  let edges = doc.edges;
  if (stepId) {
    const ids = mode === 'downstream'
      ? collectDescendantNodeIDs(doc.edges, stepId)
      : collectAncestorNodeIDs(doc.edges, stepId);
    nodes = doc.nodes.filter((n) => ids.has(n.id));
    edges = doc.edges.filter((e) => ids.has(e.source) && ids.has(e.target));
  }
  return buildRecipeFromFlow({
    name: recipeNameFromFilename(doc.recipeId),
    nodes,
    edges,
    stepData: doc.stepData,
  });
}

// The node-id set that "will re-run" for the current (stepId, mode) — used
// both to paint 'running'/'err' status on the right nodes. Upstream/whole
// mirror the ids buildRunRecipe just executed; downstream mirrors the
// descendant set instead of ancestors (a downstream run's own errors/re-runs
// belong to the step's descendants, not its ancestors).
function runTargetNodeIDs(doc: DocState, stepId: string | null, mode: 'upstream' | 'downstream'): string[] {
  if (!stepId) return doc.nodes.map((n) => n.id);
  const ids = mode === 'downstream'
    ? collectDescendantNodeIDs(doc.edges, stepId)
    : collectAncestorNodeIDs(doc.edges, stepId);
  return Array.from(ids);
}

export function RunPanel(_props: IDockviewPanelProps) {
  const active = useWorkspaceStore((s) => s.active);
  const doc = useWorkspaceStore((s) => (active ? s.docs[active] : undefined));
  const setNodeRunStatus = useWorkspaceStore((s) => s.setNodeRunStatus);
  const bumpRun = useWorkspaceStore((s) => s.bumpRun);
  const { selectedRecords, sshUser } = useHostSelection();

  // No run has been triggered yet for this doc (or there's no active doc at
  // all) — nothing to execute. Once `startRun`/`bumpRun` fire, runCount > 0
  // and stays > 0 for the doc's lifetime, so this only shows pre-first-run.
  if (!doc || doc.runCount === 0) {
    return (
      <div style={{ padding: 16, color: '#8b949e' }}>
        No active run. Use &ldquo;Run&rdquo; on a step or &ldquo;Run recipe&rdquo;.
      </div>
    );
  }

  const stepId = doc.runStepId;
  const mode = doc.runMode ?? 'upstream';
  const recipe = buildRunRecipe(doc, stepId, mode);

  return (
    <div style={{ padding: 12, height: '100%', overflowY: 'auto' }}>
      {/*
        StepRun (webui/src/RecipesTab/StepRun.tsx) reads its recipe/hosts/
        sshUser/recordSession/envOverrides from WizardContext (`useWizard()`),
        not from props — its real Props type is just the run-lifecycle
        callbacks (sessionRecordingAvailable/onViewRecording/onRunAgain/
        onStartNew/onRow/onStatusChange). A fresh WizardProvider per run
        (keyed on recipeId+stepId+mode+runCount) seeds that context with this
        run's recipe/hosts/user/env so StepRun's mount-time exec effect sees
        the right values on its very first render. recipeId must be in the
        key — otherwise two open docs whose (stepId, runCount) happen to
        coincide (e.g. both just did their first "Run recipe") would share a
        React key across an active-doc switch, so React reuses the
        WizardProvider instance instead of remounting it, and StepRun keeps
        showing the previous doc's run against the newly-active doc. mode is
        in the key too — switching "Run Step" -> "Resume from here" on the
        same step (same stepId, and runCount only bumps on a NEW trigger) must
        still remount so the WizardProvider re-seeds `edits` with the newly-
        built downstream (vs. upstream) recipe.
      */}
      <WizardProvider
        key={`${doc.recipeId}-${stepId ?? 'all'}-${mode}-${doc.runCount}`}
        initialHosts={selectedRecords}
        initialEdits={recipe as unknown as ParsedRecipe}
        initialSshUser={sshUser}
        initialRecordSession={false}
        initialEnvOverrides={doc.runExtraEnv}
      >
        <StepRun
          sessionRecordingAvailable={false}
          onViewRecording={() => {}}
          onRunAgain={() => {
            setNodeRunStatus(doc.recipeId, runTargetNodeIDs(doc, stepId, mode), 'running');
            bumpRun(doc.recipeId);
          }}
          onStartNew={() => {}}
          onRow={(row) => {
            const id = row.StepID || (stepId ?? '');
            setNodeRunStatus(doc.recipeId, [id], row.Skipped ? 'skipped' : row.Success ? 'ok' : 'err');
          }}
          onStatusChange={(status) => {
            if (status === 'err' && stepId) {
              setNodeRunStatus(doc.recipeId, runTargetNodeIDs(doc, stepId, mode), 'err');
            }
          }}
        />
      </WizardProvider>
    </div>
  );
}
