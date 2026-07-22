import type { IDockviewPanelProps } from 'dockview';
import type { ParsedRecipe } from '../../../api/types/recipes';
import type { DocState } from '../types';
import { useWorkspaceStore } from '../store';
import { StepRun } from '../../../RecipesTab/StepRun';
import { WizardProvider } from '../../../RecipesTab/WizardContext';
import { buildRecipeFromFlow, collectAncestorNodeIDs, recipeNameFromFilename } from '../../useRecipeGraph';
import { useHostSelection } from '../../../contexts/HostSelectionContext';

// Builds the recipe JSON to execute for the current run: the step + its
// ancestors when `stepId` is set ("Run Step"), or the whole doc when it's
// null ("Run recipe") — mirrors the old (pre-dockview) StudioWorkspace's
// buildSubgraphRecipe, adapted to buildRecipeFromFlow's real signature
// (`{ name, nodes, edges, stepData }` — it has no `targetStepId` param).
function buildRunRecipe(doc: DocState, stepId: string | null) {
  let nodes = doc.nodes;
  let edges = doc.edges;
  if (stepId) {
    const ids = collectAncestorNodeIDs(doc.edges, stepId);
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
  const recipe = buildRunRecipe(doc, stepId);

  return (
    <div style={{ padding: 12, height: '100%', overflowY: 'auto' }}>
      {/*
        StepRun (webui/src/RecipesTab/StepRun.tsx) reads its recipe/hosts/
        sshUser/recordSession from WizardContext (`useWizard()`), not from
        props — its real Props type is just the run-lifecycle callbacks
        (sessionRecordingAvailable/onViewRecording/onRunAgain/onStartNew/
        onRow/onStatusChange). A fresh WizardProvider per run (keyed on
        recipeId+stepId+runCount) seeds that context with this run's recipe/
        hosts/user so StepRun's mount-time exec effect sees the right values
        on its very first render. recipeId must be in the key — otherwise two
        open docs whose (stepId, runCount) happen to coincide (e.g. both just
        did their first "Run recipe") would share a React key across an
        active-doc switch, so React reuses the WizardProvider instance
        instead of remounting it, and StepRun keeps showing the previous
        doc's run against the newly-active doc.
      */}
      <WizardProvider
        key={`${doc.recipeId}-${stepId ?? 'all'}-${doc.runCount}`}
        initialHosts={selectedRecords}
        initialEdits={recipe as unknown as ParsedRecipe}
        initialSshUser={sshUser}
        initialRecordSession={false}
      >
        <StepRun
          sessionRecordingAvailable={false}
          onViewRecording={() => {}}
          onRunAgain={() => {
            const ids = stepId
              ? Array.from(collectAncestorNodeIDs(doc.edges, stepId))
              : doc.nodes.map((n) => n.id);
            setNodeRunStatus(doc.recipeId, ids, 'running');
            bumpRun(doc.recipeId);
          }}
          onStartNew={() => {}}
          onRow={(row) => {
            const id = row.StepID || (stepId ?? '');
            setNodeRunStatus(doc.recipeId, [id], row.Skipped ? 'skipped' : row.Success ? 'ok' : 'err');
          }}
          onStatusChange={(status) => {
            if (status === 'err' && stepId) {
              setNodeRunStatus(doc.recipeId, Array.from(collectAncestorNodeIDs(doc.edges, stepId)), 'err');
            }
          }}
        />
      </WizardProvider>
    </div>
  );
}
