import { Tool } from '@ag-ui/client';
import { ToolExecutor, ToolContext } from '../ToolRegistry';
import { apiGet } from '../../api/core';

export class ListBackendsTool implements ToolExecutor {
  definition: Tool = {
    name: 'list_backends',
    description: 'List all available infrastructure providers and backend names from the configuration file.',
    parameters: {
      type: 'object',
      properties: {},
      required: [],
    },
  };

  async execute(_args: Record<string, unknown>, _context: ToolContext): Promise<string> {
    const res = await apiGet('/api/v1/backends');
    if (!res.ok) {
        const j = (await res.json().catch(() => ({}))) as { error?: string };
        return `Failed to list backends: ${j.error || res.statusText}`;
    }
    const data = await res.json() as { backends?: { kind: string; name: string; hint: string }[] };
    const backends = data.backends || [];
    if (!backends.length) return 'No backends found in configuration.';
    return backends.map(b => `- Provider: ${b.kind} | Backend: ${b.name} | Hint: ${b.hint}`).join('\n');
  }
}
