import type { HostRecord } from '../../HostPicker';

export type ParsedRecipeStepTemplate = {
  template: string;
  data?: Record<string, unknown>;
  output?: string;
};

export interface LogsStreamRequest {
  records: HostRecord[];
  ssh_user?: string;
  source?: string;
  follow?: boolean;
  tail?: number;
  since?: string;
  container?: string;
  unit?: string;
  command?: string;
  run_as?: string;
  grep?: string;
  labels?: string[];
  anomaly?: boolean;
  anomaly_threshold?: number;
  anomaly_only?: boolean;
  anomaly_model?: string;
  anomaly_tokenizer?: string;
  anomaly_endpoint?: string;
  anomaly_llm_model?: string;
  anomaly_context?: number;
  anomaly_filter_threshold?: number;
  anomaly_freq_window?: number;
  anomaly_freq_ratio?: number;
  anomaly_preprocessor?: string;
}

export interface LogsDefaultsResponse {
  anomaly: boolean;
  anomaly_threshold: number;
  anomaly_only: boolean;
  anomaly_model?: string;
  anomaly_tokenizer?: string;
  anomaly_endpoint?: string;
  anomaly_llm_model?: string;
  anomaly_context_lines: number;
  anomaly_filter_threshold: number;
  anomaly_freq_window: number;
  anomaly_freq_ratio: number;
  anomaly_window?: number;
  anomaly_strict?: boolean;
  anomaly_feedback_file?: string;
  alert_enabled?: boolean;
  alert_suppress_duration?: string;
  anomaly_preprocessor?: string;
}

export type FeedbackRecord = {
  ts: string;
  source: string;
  line: string;
  score: number;
  reason: string;
  anomaly: boolean;
};

export type FeedbackSuggestResponse = {
  anomaly: boolean;
  score: number;
  reason: string;
};

export type LogTemplateStat = {
  template: string;
  count: number;
  score: number;
};