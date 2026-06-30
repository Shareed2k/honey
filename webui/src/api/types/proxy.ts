

export interface AppConfig {
  name: string;
  type: string;
  mode?: string;
  target?: string;
  target_regex?: string;
  target_recipe?: string;
  backend?: string;
  provider?: string;
  upstream: string;
  local_port: number;
  ttl: number;
  open_browser: boolean;
  webhooks?: string[];
}

export interface ProxySession {
  id: string;
  app: AppConfig;
  local_addr: string;
  started_at: string;
  expires_at: string;
  pid: number;
}