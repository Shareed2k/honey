/* eslint-disable @typescript-eslint/no-explicit-any */
import React, { lazy, Suspense } from 'react';
import { Form, Input, Switch, InputNumber, Select, Collapse } from 'antd';
import KeyValueEditor from './KeyValueEditor';

const CodeEditor = lazy(() => import('../CodeEditor'));

const { Panel } = Collapse;

type Props = {
  schema: any;
  value: any;
  onChange: (next: any) => void;
};

export default function DynamicStepForm({ schema, value, onChange }: Props) {
  if (!schema || !schema.properties) {
    return <p>Select a node to edit parameters.</p>;
  }

  const renderFormItem = (key: string, propSchema: any, val: any, path: string[]) => {
    const itemPath = [...path, key];
    const itemLabel = propSchema.description || key;

    const handleFieldChange = (fieldValue: any) => {
      const updateValue = (obj: any, keys: string[], v: any): any => {
        if (keys.length === 0) return v;
        const [current, ...rest] = keys;
        const target = obj || {};
        return {
          ...target,
          [current]: updateValue(target[current], rest, v),
        };
      };
      onChange(updateValue(value, itemPath, fieldValue));
    };

    // 1. String Enums mapped to Select or Radio
    if (propSchema.enum) {
      return (
        <Form.Item key={key} label={itemLabel}>
          <Select
            value={val}
            onChange={handleFieldChange}
            options={propSchema.enum.map((v: any) => ({ value: v, label: v }))}
          />
        </Form.Item>
      );
    }

    // 1.5. Advanced KeyValueEditor for arbitrary map[string]string types (env, secrets)
    if (propSchema.type === 'object' && propSchema.additionalProperties && propSchema.additionalProperties.type === 'string') {
      return (
        <Form.Item key={key} label={itemLabel}>
          <KeyValueEditor value={val || {}} onChange={handleFieldChange} />
        </Form.Item>
      );
    }

    // 2. Objects mapped recursively
    if (propSchema.type === 'object') {
      return (
        <Collapse key={key} style={{ marginBottom: 12 }}>
          <Panel header={key} key="1">
            {Object.entries(propSchema.properties || {}).map(([subKey, subSchema]: [string, any]) =>
              renderFormItem(subKey, subSchema, val?.[subKey], itemPath)
            )}
          </Panel>
        </Collapse>
      );
    }

    // 3. Dynamic CodeEditor for command/script fields with real-time linting
    if (key === 'command' || key === 'sql') {
      const editorLang = key === 'sql' ? 'plain' : 'bash';
      return (
        <Form.Item key={key} label={itemLabel}>
          <div style={{ border: '1px solid #d9d9d9', borderRadius: 4, overflow: 'hidden' }}>
            <Suspense fallback={<div>Loading editor...</div>}>
              <CodeEditor
                value={val || ''}
                onChange={handleFieldChange}
                language={editorLang}
                lint={key === 'command'}
                height="180px"
              />
            </Suspense>
          </div>
        </Form.Item>
      );
    }

    // 4. Primitive Types
    switch (propSchema.type) {
      case 'string':
        return (
          <Form.Item key={key} label={itemLabel}>
            <Input value={val || ''} onChange={(e) => handleFieldChange(e.target.value)} />
          </Form.Item>
        );
      case 'boolean':
        return (
          <Form.Item key={key} label={itemLabel} valuePropName="checked">
            <Switch checked={!!val} onChange={handleFieldChange} />
          </Form.Item>
        );
      case 'integer':
      case 'number':
        return (
          <Form.Item key={key} label={itemLabel}>
            <InputNumber style={{ width: '100%' }} value={val} onChange={handleFieldChange} />
          </Form.Item>
        );
      default:
        return null;
    }
  };

  return (
    <Form layout="vertical">
      {Object.entries(schema.properties).map(([key, propSchema]: [string, any]) =>
        renderFormItem(key, propSchema, value?.[key], [])
      )}
    </Form>
  );
}
