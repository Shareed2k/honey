import React, { useEffect, useMemo } from 'react';
import { Modal, Form, Input, Select, Checkbox } from 'antd';
import type { RecipePrompt } from '../api/types/recipes';

type Props = {
  open: boolean;
  prompts: Record<string, RecipePrompt>;
  onCancel: () => void;
  onSubmit: (values: Record<string, string>) => void;
};

export function ParameterPromptModal({ open, prompts, onCancel, onSubmit }: Props) {
  const [form] = Form.useForm();

  const initialValues = useMemo(() => {
    const vals: Record<string, any> = {};
    for (const [key, def] of Object.entries(prompts)) {
      if (def.type === 'boolean') {
        vals[key] = def.default === 'true';
      } else if (def.multi && def.default) {
        vals[key] = def.default.split(',').map((s) => s.trim());
      } else if (def.default !== undefined) {
        vals[key] = def.default;
      }
    }
    return vals;
  }, [prompts]);

  useEffect(() => {
    if (open) {
      form.resetFields();
      form.setFieldsValue(initialValues);
    }
  }, [open, form, initialValues]);

  const handleFinish = (values: Record<string, any>) => {
    // Transform arrays back to comma-separated strings, and booleans to "true"/"false" strings
    const transformed: Record<string, string> = {};
    for (const [k, v] of Object.entries(values)) {
      if (Array.isArray(v)) {
        transformed[k] = v.join(',');
      } else if (typeof v === 'boolean') {
        transformed[k] = v ? 'true' : 'false';
      } else if (v !== undefined && v !== null) {
        transformed[k] = String(v);
      } else {
        transformed[k] = '';
      }
    }
    onSubmit(transformed);
  };

  return (
    <Modal
      title="Recipe Parameters Required"
      open={open}
      onCancel={onCancel}
      onOk={() => form.submit()}
      okText="Continue"
    >
      <Form form={form} layout="vertical" onFinish={handleFinish}>
        {Object.entries(prompts).map(([key, def]) => {
          const rules: any[] = [];
          if (def.required) {
            rules.push({ required: true, message: `Please input ${key}` });
          }
          if (def.regex) {
            rules.push({ pattern: new RegExp(def.regex), message: `Format invalid (must match ${def.regex})` });
          }

          let inputComponent = <Input />;
          if (def.type === 'boolean') {
            // Checkbox value prop is 'checked', so we need valuePropName
            return (
              <Form.Item
                key={key}
                name={key}
                valuePropName="checked"
                tooltip={def.description}
              >
                <Checkbox>{key}</Checkbox>
              </Form.Item>
            );
          } else if (def.choices && def.choices.length > 0) {
            inputComponent = (
              <Select mode={def.multi ? 'multiple' : undefined} allowClear>
                {def.choices.map((c) => (
                  <Select.Option key={c} value={c}>
                    {c}
                  </Select.Option>
                ))}
              </Select>
            );
          } else if (def.type === 'password') {
            inputComponent = <Input.Password />;
          }

          return (
            <Form.Item
              key={key}
              name={key}
              label={key}
              rules={rules}
              tooltip={def.description}
            >
              {inputComponent}
            </Form.Item>
          );
        })}
      </Form>
    </Modal>
  );
}