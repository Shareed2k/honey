import { HostExecResultRow } from './exec';
import { ParsedRecipeCloudBackendRef } from './recipes';



export type ParsedRecipeFileTransfer = {
  local: string;
  remote: string;
  path?: string;
  body?: string;
};

export type ParsedRecipeAgentTransferCloud = {
  provider: string;
  bucket: string;
  prefix?: string;
  object?: string;
  region?: string;
  endpoint?: string;
};

export type ParsedRecipeAgentTransfer = {
  dest_host: string;
  source_path: string;
  dest_path: string;
  cloud: ParsedRecipeAgentTransferCloud;
  cloud_backend_ref?: ParsedRecipeCloudBackendRef;
  keep_object?: boolean;
  max_retries?: number;
  agent_remote_dir?: string;
};

export type FileBrowserEntry = {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  mode: string;
  modified_at: string;
};

export type AgentTransferCloud = {
  provider: string;
  bucket: string;
  prefix?: string;
  object?: string;
  region?: string;
  endpoint?: string;
};

export type AgentTransferBackendRef = {
  kind: string;
  name?: string;
  index?: number;
};

export type AgentTransferEvent = {
  stage: string;
  host?: string;
  success: boolean;
  message?: string;
  error?: string;
  attempt?: number;
  timestamp: string;
};

/** Bytes sent to this origin; then server may still work (e.g. SFTP to a host). */
export type FormDataUploadProgressEvent =
  | { kind: 'uploading'; loaded: number; total: number | null }
  | { kind: 'awaiting_response' };

/** Server-sent upload stream after the multipart body is stored (SFTP byte progress). */
export type UploadStreamServerEvent =
  | { phase: 'sftp_start'; total_bytes: number }
  | { phase: 'sftp'; sent_bytes: number; total_bytes: number }
  | { phase: 'error'; message?: string; result?: HostExecResultRow }
  | { phase: 'done'; results: HostExecResultRow[] };