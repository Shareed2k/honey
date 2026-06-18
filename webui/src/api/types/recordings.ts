import { HostExecResultRow } from './exec';



export type RecordingsRetentionInfo = {
  enabled: boolean;
  max_age?: string;
};

export type RecordingsListResponse = {
  items: RecordingListEntry[];
  file_count: number;
  total_bytes: number;
  retention?: RecordingsRetentionInfo;
};

export type RecordingListEntry = {
  file_name: string;
  modified_unix_ms: number;
  size_bytes: number;
  trigger?: string;
  mode?: string;
  provider?: string;
  host_name?: string;
  host_ip?: string;
  user?: string;
};

export type RecordingEvent = {
  time_ms: number;
  type: string;
  direction?: string;
  data_b64?: string;
  cols?: number;
  rows?: number;
  message?: string;
  /** Batch exec / CUE: JSON object matching HostExecResultRow when type is "result". */
  result?: HostExecResultRow;
};