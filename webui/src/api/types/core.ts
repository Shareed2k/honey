import { LibraryRecipe } from './recipes';



export type ResolvedStep = {
  index: number;
  id?: string;
  depends?: string[];
  wave?: number;
  kind: string;
  host: string;
  run_as?: string;
  when?: string;
  retry?: string;
  notify?: boolean;
  preview: string;
};

export type GraphPlanNode = {
  index: number;
  id: string;
  kind: string;
  host: string;
  wave?: number;
  when?: string;
  retry?: string;
  notify?: boolean;
  kv_tunnel?: boolean;
  preview?: string;
};

export type GraphPlanEdge = {
  from: string;
  to: string;
};

export type RiskReport = {
  score: number;
  level: string;
  findings: string[];
};

export type LibraryCategory = {
  name: string;
  recipes: LibraryRecipe[];
};