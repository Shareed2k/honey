// webui/src/RecipesTab/types.ts
import type { HostRecord } from '../HostPicker';
import type { HostExecResultRow } from '../api/types/exec';
import type { ParsedRecipe, RecipeGraphPlan, ValidationError } from '../api/types/recipes';
import type { ResolvedStep, RiskReport } from '../api/types/core';

export type WizardStep = 1 | 2 | 3 | 4;

export type RecipeRef =
  | { kind: 'disk'; path: string }
  | { kind: 'draft'; id: string };

export type Draft = {
  id: string;
  name: string;
  baseRecipePath: string;
  modifiedAt: string;
  recipe: ParsedRecipe;
};

export type EnvPair = { key: string; value: string };

export type PlanState =
  | { ok: true; plan: string; steps: ResolvedStep[]; graph?: RecipeGraphPlan; risk?: RiskReport }
  | { ok: false; errors: ValidationError[] }
  | null;

export type LiveState = {
  rows: HostExecResultRow[];
  status: 'idle' | 'running' | 'ok' | 'err' | 'cancelled';
};

export type WizardState = {
  step: WizardStep;
  hosts: HostRecord[];
  recipe: RecipeRef | null;
  edits: ParsedRecipe | null;
  envOverrides: EnvPair[];
  sshUser: string;
  recordSession: boolean;
  plan: PlanState;
  live: LiveState;
};

export const INITIAL_WIZARD_STATE: WizardState = {
  step: 1,
  hosts: [],
  recipe: null,
  edits: null,
  envOverrides: [],
  sshUser: 'root',
  recordSession: true,
  plan: null,
  live: { rows: [], status: 'idle' },
};
