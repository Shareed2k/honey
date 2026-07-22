export type RunStatus = 'running' | 'ok' | 'err' | 'skipped';

export interface Validation {
  state: 'idle' | 'validating' | 'valid' | 'invalid';
  issues: { path?: string; kind?: string; message: string }[];
  risk?: unknown;
}

export interface DocState {
  recipeId: string;      // saved recipe filename, or "untitled-N"
  name: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  nodes: any[];
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  edges: any[];
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  stepData: Record<string, any>;
  recipeDefaults: unknown;
  selectedNodeId: string | null;
  rawMode: boolean;
  rawContent: string;
  originalCue: string;
  validation: Validation;
  runStatus: Record<string, RunStatus>;
  dirty: boolean;
}

export interface PersistedWorkspace {
  layout: unknown;
  openRecipes: string[];
  active: string | null;
}
