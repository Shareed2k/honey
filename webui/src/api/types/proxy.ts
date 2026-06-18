

export interface AppConfig {
  name: string;
  type: string;
  mode?: string;
  target?: string;
  target_regex?: string;
  backend?: string;
  provider?: string;
  upstream: string;
  local_port: number;
  ttl: number;
  open_browser: boolean;
}

export interface ProxySession {
  id: string;
  app: AppConfig;
  local_addr: string;
  started_at: string;
  expires_at: string;
  pid: number;
}