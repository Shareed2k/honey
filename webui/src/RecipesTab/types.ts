// webui/src/RecipesTab/types.ts
import type { HostRecord } from '../HostPicker';
import type {
  HostExecResultRow,
  ParsedRecipe,
  ResolvedStep,
  ValidationError,
} from '../api';

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
  | { ok: true; plan: string; steps: ResolvedStep[] }
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
