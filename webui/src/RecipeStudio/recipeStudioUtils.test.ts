import { describe, expect, it } from 'vitest';

import {
  applyWaveLayout,
  buildRecipeFromFlow,
  createStepDraft,
  detectStepKind,
  listStepKinds,
  recipeNameFromFilename,
  stepSchemaForKind,
} from './recipeStudioUtils';

describe('recipeStudioUtils', () => {
  it('lists step kinds from backend per-kind definitions, excluding defaults', () => {
    const schema = {
      definitions: {
        postgres: { type: 'object' },
        opensearch: { type: 'object' },
        command: { type: 'object' },
        defaults: { type: 'object' },
      },
    };

    expect(listStepKinds(schema).map((kind) => kind.kind)).toEqual([
      'command',
      'postgres',
      'opensearch',
    ]);
  });

  it('detects a step kind from any populated module field', () => {
    expect(detectStepKind({ host: '*', tunnel: { remote_host: 'localhost' } })).toBe('tunnel');
    expect(detectStepKind({ host: '*', plugin: { id: 'service', action: 'restart' } })).toBe('plugin');
    expect(detectStepKind({ host: '*', command: 'echo hi' })).toBe('command');
  });

  it('creates drafts with the selected kind initialized where needed', () => {
    expect(createStepDraft('plugin', 'plugin_1')).toEqual({
      id: 'plugin_1',
      kind: 'plugin',
      host: '*',
      plugin: {},
    });
    expect(createStepDraft('command', 'command_1')).toEqual({
      id: 'command_1',
      kind: 'command',
      host: '*',
      command: '',
    });
  });

  it('returns the self-contained per-kind definition for the selected kind', () => {
    const schema = {
      definitions: {
        command: { type: 'object', properties: { id: { type: 'string' }, command: { type: 'string' } } },
        postgres: {
          type: 'object',
          properties: {
            id: { type: 'string' },
            host: { type: 'string' },
            postgres: { $ref: '#/$defs/RecipeStepPostgres' },
          },
          $defs: { RecipeStepPostgres: { type: 'object', properties: { sql: { type: 'string' } } } },
        },
      },
    };

    // The postgres step schema exposes base fields plus its own action object,
    // with the internal $ref inlined so the form can render it.
    expect(stepSchemaForKind(schema, 'postgres')).toEqual({
      type: 'object',
      properties: {
        id: { type: 'string' },
        host: { type: 'string' },
        postgres: { type: 'object', properties: { sql: { type: 'string' } } },
      },
    });
  });

  it('inlines nested $ref entries and drops the $defs table', () => {
    const schema = {
      definitions: {
        command: {
          type: 'object',
          properties: {
            command: { type: 'string' },
            retry: { $ref: '#/$defs/RecipeStepRetry' },
          },
          $defs: { RecipeStepRetry: { type: 'object', properties: { attempts: { type: 'integer' } } } },
        },
      },
    };

    const resolved = stepSchemaForKind(schema, 'command');
    expect(resolved.$defs).toBeUndefined();
    expect(resolved.properties.retry).toEqual({ type: 'object', properties: { attempts: { type: 'integer' } } });
  });

  it('serializes dynamic step fields and removes ui-only kind', () => {
    const recipe = buildRecipeFromFlow({
      name: 'dynamic-demo',
      nodes: [
        { id: 'plugin_1' },
        { id: 'verify' },
      ],
      edges: [{ source: 'plugin_1', target: 'verify' }],
      stepData: {
        plugin_1: { id: 'plugin_1', kind: 'plugin', host: '*', plugin: { id: 'service', action: 'restart' } },
        verify: { id: 'verify', kind: 'command', host: '*', command: 'true' },
      },
    });

    expect(recipe.steps).toEqual([
      { id: 'plugin_1', host: '*', plugin: { id: 'service', action: 'restart' } },
      { id: 'verify', host: '*', depends: ['plugin_1'], command: 'true' },
    ]);
  });

  it('derives recipe name from cue filenames only', () => {
    expect(recipeNameFromFilename('rolling-restart-kafka.cue')).toBe('rolling-restart-kafka');
    expect(recipeNameFromFilename('ops.yaml')).toBe('ops.yaml');
    expect(recipeNameFromFilename('ops.yml')).toBe('ops.yml');
    expect(recipeNameFromFilename('ops.json')).toBe('ops.json');
    expect(recipeNameFromFilename(undefined)).toBe('visual-studio-recipe');
  });

  it('preserves selected empty kind field when saving a draft node', () => {
    const recipe = buildRecipeFromFlow({
      name: 'draft-demo',
      nodes: [{ id: 'plugin_1' }],
      edges: [],
      stepData: {
        plugin_1: { id: 'plugin_1', kind: 'plugin', host: '*', plugin: {} },
      },
    });

    expect(recipe.steps).toEqual([{ id: 'plugin_1', host: '*', plugin: {} }]);
  });

  it('places nodes in the same wave in the same column', () => {
    const nodes = [
      { id: 'fetch', position: { x: 0, y: 0 }, data: { wave: 1 } },
      { id: 'restart_api', position: { x: 0, y: 0 }, data: { wave: 2 } },
      { id: 'restart_worker', position: { x: 0, y: 0 }, data: { wave: 2 } },
      { id: 'verify', position: { x: 0, y: 0 }, data: { wave: 3 } },
    ];

    const laidOut = applyWaveLayout(nodes);

    expect(laidOut[0].position).toEqual({ x: 100, y: 80 });
    expect(laidOut[1].position).toEqual({ x: 340, y: 80 });
    expect(laidOut[2].position).toEqual({ x: 340, y: 190 });
    expect(laidOut[3].position).toEqual({ x: 580, y: 80 });
  });

  it('keeps nodes without waves in an end column', () => {
    const nodes = [
      { id: 'known', position: { x: 10, y: 10 }, data: { wave: 1 } },
      { id: 'draft', position: { x: 20, y: 20 }, data: {} },
    ];

    const laidOut = applyWaveLayout(nodes);

    expect(laidOut[0].position).toEqual({ x: 100, y: 80 });
    expect(laidOut[1].position).toEqual({ x: 340, y: 80 });
  });
});
