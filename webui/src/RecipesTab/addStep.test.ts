import { describe, expect, it } from 'vitest';
import { appendRecipeStep } from './recipeStepUtils';

describe('appendRecipeStep graph ids', () => {
  it('uses incremental step ids in graph recipes', () => {
    const recipe = {
      name: 'g',
      type: 'graph',
      steps: [{ host: '*', id: 'step_1', command: 'a' }],
    };
    const next = appendRecipeStep(recipe, 'command');
    expect(next.steps[1].id).toBe('step_2');
  });
});
