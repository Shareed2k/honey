

export type TunnelInfo = {
  id: string;
  host: string;
  record_key: string;
  mapping: string;
  started_at: string;
  error?: string;
};

export type ParsedRecipeTunnel = {
  mode?: string;
  remote_host?: string;
  remote_port?: number;
  local_port?: number;
  bind?: string;
  use_ssh_config?: boolean;
  ssh_config_match?: string;
  share_key?: string;
  protocol?: string;
  remote_bind?: string;
  remote_listen_port?: number;
  local_host?: string;
  local_target_port?: number;
  tun_local?: number;
  tun_remote?: number;
  remote_socat?: boolean;
};