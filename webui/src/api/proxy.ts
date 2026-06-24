import { apiGet, apiPost, apiDelete } from './core';
import { ProxySession } from './types/proxy';



export async function fetchProxySessions(): Promise<ProxySession[]> {
  const res = await apiGet('/api/v1/proxy/sessions');
  if (!res.ok) {
    throw new Error(res.statusText);
  }
  const data = await res.json();
  return data.sessions || [];
}

export async function startProxySession(appName: string, sshUser: string, providers: string[], backends: string[]): Promise<ProxySession> {
  const res = await apiPost('/api/v1/proxy/start', { 
    app: appName, 
    ssh_user: sshUser,
    providers: providers.join(','),
    backends: backends.join(',')
  });
  if (!res.ok) {
    const errorText = await res.text();
    throw new Error(errorText || res.statusText);
  }
  return await res.json();
}

export async function stopProxySession(id: string): Promise<void> {
  const res = await apiDelete(`/api/v1/proxy/sessions/${id}`);
  if (!res.ok) {
    const errorText = await res.text();
    throw new Error(errorText || res.statusText);
  }
}