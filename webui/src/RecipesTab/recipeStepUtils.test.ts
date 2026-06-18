import { describe, expect, it } from 'vitest';
import type { ParsedRecipeStep } from '../api/types/recipes';
import { appendRecipeStep, defaultStepForKind, stepKind, stepSupportsNotify } from './recipeStepUtils';

describe('stepKind', () => {
  it('detects primary kinds', () => {
    expect(stepKind({ host: '*', command: 'x' })).toBe('command');
    expect(stepKind({ host: '*', script: { local: 'a', remote: 'b' } })).toBe('script');
    expect(stepKind({ host: '*', plugin: { id: 'p', action: 'a' } })).toBe('plugin');
    expect(stepKind({ host: '*', put: { local: '', remote: '' } })).toBe('put');
    expect(stepKind({ host: '*', tunnel: { remote_port: 1 } })).toBe('tunnel');
  });

  it('does not treat notify as primary kind', () => {
    const s: ParsedRecipeStep = {
      host: '*',
      command: 'echo',
      notify: { message: 'hi' },
    };
    expect(stepKind(s)).toBe('command');
  });
});

describe('defaultStepForKind', () => {
  it('assigns graph id and depends', () => {
    const s = defaultStepForKind('command', { graph: true, stepNumber: 2 });
    expect(s.id).toBe('step_2');
    expect(s.depends).toEqual([]);
    expect(s.command).toBe('echo ok');
  });
});

describe('appendRecipeStep', () => {
  it('appends a step', () => {
    const recipe = { name: 't', steps: [{ host: '*', command: 'a' }] };
    const next = appendRecipeStep(recipe, 'put');
    expect(next.steps).toHaveLength(2);
    expect(stepKind(next.steps[1])).toBe('put');
  });
});

describe('stepSupportsNotify', () => {
  it('includes command and plugin', () => {
    expect(stepSupportsNotify('command')).toBe(true);
    expect(stepSupportsNotify('tunnel')).toBe(false);
  });
});
