import { Tool } from '@ag-ui/client';
import { ToolExecutor, ToolContext } from '../ToolRegistry';
import { apiPost } from '../../api/core';
import type { HostRecord } from '../../HostPicker';

export class SearchHostsTool implements ToolExecutor {
  definition: Tool = {
    name: 'search_hosts',
    description: 'Search infrastructure hosts by name, provider, or backend.',
    parameters: {
      type: 'object',
      properties: { 
        name: { type: 'string', description: 'Substring to match against host names.' },
        providers: { type: 'string', description: 'Comma-separated list of providers (e.g. docker,aws,proxmox).' },
        backends: { type: 'string', description: 'Comma-separated list of backend names.' },
      },
      required: [],
    },
  };

  async execute(args: Record<string, unknown>, _context: ToolContext): Promise<string> {
    const reqBody: Record<string, unknown> = { name: args.name ?? '' };
    if (args.providers) reqBody.providers = args.providers;
    if (args.backends) reqBody.backends = args.backends;
    
    const res = await apiPost('/api/v1/search', reqBody);
    if (!res.ok) {
        const j = (await res.json().catch(() => ({}))) as { error?: string };
        return `Failed to search: ${j.error || res.statusText}`;
    }
    const data = await res.json() as { records?: HostRecord[] };
    const hosts = data.records || [];
    if (!hosts.length) return 'No hosts found.';
    return hosts.map((h: HostRecord) => `${h.name} (${h.primary_ip}, ${h.provider})`).join('\n');
  }
}
