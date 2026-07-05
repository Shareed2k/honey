import { Tool } from '@ag-ui/client';
import { ToolExecutor, ToolContext } from '../ToolRegistry';
import { apiPost } from '../../api/core';

export class ValidateRecipeTool implements ToolExecutor {
  definition: Tool = {
    name: 'validate_recipe',
    description: 'Validate a Honey CUE recipe.',
    parameters: {
      type: 'object',
      properties: { recipe_content_raw: { type: 'string' } },
      required: ['recipe_content_raw'],
    },
  };

  async execute(args: Record<string, unknown>, _context: ToolContext): Promise<string> {
    const res = await apiPost('/api/v1/recipes/validate-content', { recipe_content_raw: args.recipe_content_raw });
    if (!res.ok) {
        const j = (await res.json().catch(() => ({}))) as { error?: string };
        return `Failed to validate: ${j.error || res.statusText}`;
    }
    const data = await res.json() as { errors?: string[] };
    return data.errors?.length ? `Errors:\n${data.errors.join('\n')}` : 'Recipe is valid.';
  }
}
