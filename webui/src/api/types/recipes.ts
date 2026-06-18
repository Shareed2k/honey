import { ParsedRecipeStepTemplate } from './logs';
import { ParsedRecipeFileTransfer, ParsedRecipeAgentTransfer } from './files';
import { ParsedRecipeTunnel } from './tunnels';
import { GraphPlanNode, GraphPlanEdge } from './core';



export type RecipeListEntry = { name: string; path: string };

export type RecipePrompt = {
  description?: string;
  type?: string;
  required?: boolean;
  choices?: string[];
  default?: string;
  multi?: boolean;
  regex?: string;
};

export /** Structured recipe shape that mirrors internal/cuetry.Recipe (JSON keys match Go json tags). */
export type ParsedRecipe = {
  name: string;
  type?: string;
  defaults?: {
    prompts?: Record<string, RecipePrompt>;
    [key: string]: unknown;
  };
  steps: ParsedRecipeStep[];
};

export type ParsedRecipeEnvFrom = {
  step?: string;
  from_output?: string;
  map: Record<string, string>;
};

export type ParsedRecipeStepRetry = {
  attempts?: number;
  delay_ms?: number;
  max_delay_ms?: number;
  backoff?: 'fixed' | 'exponential';
};

export type ParsedRecipePlugin = {
  id: string;
  action: string;
  config?: Record<string, unknown>;
};

export type ParsedRecipeCloudBackendRef = {
  kind: string;
  name?: string;
  index?: number;
};

export type ParsedRecipeNotifyServices = {
  http?: Record<string, never>;
  slack?: { channel_id?: string };
  telegram?: Record<string, never>;
};

export type ParsedRecipeNotify = {
  notify_subject?: string;
  message?: string;
  services?: ParsedRecipeNotifyServices;
};

export type ParsedRecipeStep = {
  id?: string;
  depends?: string[];
  host: string;
  command?: string;
  script?: ParsedRecipeFileTransfer;
  ai?: { model?: string; prompt?: string; system_prompt?: string; max_output_tokens?: number; max_input_chars?: number };
  template?: ParsedRecipeStepTemplate;
  put?: ParsedRecipeFileTransfer;
  get?: ParsedRecipeFileTransfer;
  plugin?: ParsedRecipePlugin;
  tunnel?: ParsedRecipeTunnel;
  agent_transfer?: ParsedRecipeAgentTransfer;
  notify?: ParsedRecipeNotify;
  run_as?: string;
  env?: Record<string, string>;
  max_parallel?: number;
  kv_tunnel?: boolean;
  env_from?: ParsedRecipeEnvFrom[];
  when?: string;
  retry?: ParsedRecipeStepRetry;
  hooks?: { on_success?: ParsedRecipeStep; on_failure?: ParsedRecipeStep };
};

export type RecipeGraphPlan = {
  type: string;
  waves?: GraphPlanNode[][];
  nodes: GraphPlanNode[];
  edges: GraphPlanEdge[];
  mermaid?: string;
};

export type ValidationError = {
  path?: string;
  kind: 'json' | 'schema' | 'validation' | 'resolve';
  message: string;
};

export type RecentRunEntry = {
  recipe_name: string;
  recipe_path: string;
  host_count: number;
  started_at: string;
  recording_id: string;
  recipe_content_hash?: string;
  edited: boolean;
  hosts?: HostRecord[];
};

export type RecipesAIGraphResponse = {
  recipe: Record<string, unknown>;
  explanation?: string;
};

export type LibraryRecipe = {
  name: string;
  filename: string;
  description: string;
  content: string;
  category: string;
};