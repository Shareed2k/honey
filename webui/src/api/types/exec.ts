import { ParsedRecipe } from './recipes';



/** Matches Go ui.HostExecResult JSON (exported struct fields). */
export type HostExecResultRow = {
  Name: string;
  IP: string;
  Provider: string;
  Success: boolean;
  Skipped?: boolean;
  StepIndex?: number;
  StepID?: string;
  StepKind?: string;
  ExitCode: number;
  Output: string;
  ErrMsg: string;
  HookPhase?: string;
  HookOutput?: string;
};

export type ExecOnHostsBody = {
  ssh_user: string;
  command: string;
  records: unknown[];
  record_session?: boolean;
  run_as?: string;
  exec_mode?: 'command' | 'script';
  script_interpreter?: string;
  interpreter_args_quoted?: boolean;
  file_extension?: string;
  remove_tmp_file?: boolean;
  script_args?: string[];
  timeout?: string; // per-host command timeout, e.g. "30s"; empty uses server default
};

export type LintDiagnostic = {
  line: number;
  col: number;
  severity: 'error' | 'warning';
  message: string;
};

export type LintResponse = {
  available: boolean;
  tool?: string;
  diagnostics: LintDiagnostic[];
};

export type ExecSnippet = {
  id: string;
  name: string;
  mode: 'command' | 'script';
  command: string;
  script_interpreter?: string;
  interpreter_args_quoted?: boolean;
  run_as?: string;
};

export type CueExecRequest = {
  recipe_path?: string;
  recipe_content?: ParsedRecipe;
  execute: boolean;
  ssh_user: string;
  records: unknown[];
  env?: string[];
  record_session?: boolean;
  timeout?: string; // per-host command timeout, e.g. "30s"; empty uses server default
};