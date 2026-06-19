import React, { useEffect, useMemo, useState } from 'react';
import { Modal, Form, Input, Select, Checkbox, Upload, Button, message, Space } from 'antd';
import { UploadOutlined, ClearOutlined } from '@ant-design/icons';
import type { RecipePrompt } from '../api/types/recipes';
import { apiHeaders } from '../api/core';
import { JSONPath } from 'jsonpath-plus';

type Props = {
  open: boolean;
  prompts: Record<string, RecipePrompt>;
  recipeName?: string;
  onCancel: () => void;
  onSubmit: (values: Record<string, string>) => void;
};

function RemoteSelect({ prompt, value, onChange }: { prompt: RecipePrompt; value?: any; onChange?: (val: any) => void }) {
  const [options, setOptions] = useState<{label: string, value: string}[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!prompt.choices_url) return;
    setLoading(true);
    fetch('/api/v1/recipes/prompts/choices', {
      method: 'POST',
      headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: prompt.choices_url })
    })
      .then(res => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
      })
      .then(data => {
        let results = data;
        if (prompt.choices_json_path) {
          results = JSONPath({ path: prompt.choices_json_path, json: data });
          if (Array.isArray(results) && results.length === 1 && Array.isArray(results[0])) {
            results = results[0]; // flatten if jsonpath returns array of array
          }
        }
        const mapped = Array.isArray(results) ? results.map(item => {
          if (typeof item === 'object' && item !== null) {
            return { label: item.name || item.value || JSON.stringify(item), value: item.value || JSON.stringify(item) };
          }
          return { label: String(item), value: String(item) };
        }) : [];
        setOptions(mapped);
      })
      .catch(err => {
        message.error(`Failed to load choices: ${err.message}`);
      })
      .finally(() => setLoading(false));
  }, [prompt.choices_url, prompt.choices_json_path]);

  return (
    <Select mode={prompt.multi ? 'multiple' : undefined} allowClear loading={loading} value={value} onChange={onChange} options={options} />
  );
}

function FileUpload({ value, onChange }: { value?: any; onChange?: (val: any) => void }) {
  return (
    <Upload
      name="file"
      action="/api/v1/recipes/prompts/upload"
      headers={apiHeaders()}
      maxCount={1}
      onChange={(info) => {
        if (info.file.status === 'done') {
          onChange?.(info.file.response);
        } else if (info.file.status === 'error') {
          message.error(`${info.file.name} file upload failed.`);
        } else if (info.file.status === 'removed') {
          onChange?.(null);
        }
      }}
      fileList={value ? [
        {
          uid: value.id,
          name: value.filename,
          status: 'done' as const,
          response: value,
        }
      ] : []}
    >
      <Button icon={<UploadOutlined />}>Click to Upload</Button>
    </Upload>
  );
}

export function ParameterPromptModal({ open, prompts, recipeName, onCancel, onSubmit }: Props) {
  const [form] = Form.useForm();

  const getInitialValues = () => {
    const vals: Record<string, any> = {};
    const params = new URLSearchParams(window.location.search);
    let cached: Record<string, any> = {};
    if (recipeName) {
      try {
        cached = JSON.parse(localStorage.getItem(`honey_prompt_cache_${recipeName}`) || '{}');
      } catch (e) {}
    }

    for (const [key, def] of Object.entries(prompts)) {
      let val = def.default;
      if (cached[key] !== undefined) val = cached[key];
      
      const qVal = params.get(`prompt.${key}`);
      if (qVal !== null) val = qVal;

      if (def.type === 'boolean') {
        vals[key] = val === 'true' || val === true;
      } else if (def.multi && typeof val === 'string') {
        vals[key] = val.split(',').map((s) => s.trim());
      } else if (val !== undefined) {
        vals[key] = val;
      }
    }
    return vals;
  };

  useEffect(() => {
    if (open) {
      form.resetFields();
      form.setFieldsValue(getInitialValues());
    }
  }, [open, form, prompts, recipeName]);

  const handleFinish = (values: Record<string, any>) => {
    if (recipeName) {
      const toCache: Record<string, any> = {};
      for (const [k, v] of Object.entries(values)) {
        if (prompts[k]?.type !== 'file' && prompts[k]?.type !== 'password') {
          toCache[k] = v;
        }
      }
      localStorage.setItem(`honey_prompt_cache_${recipeName}`, JSON.stringify(toCache));
    }

    const transformed: Record<string, string> = {};
    for (const [k, v] of Object.entries(values)) {
      const def = prompts[k];
      if (def?.type === 'file' && v && v.id) {
        transformed[`HONEY_PROMPT_${k}`] = String(v.id);
        transformed[`HONEY_FILE_${k}`] = String(v.path);
        transformed[`HONEY_FILE_${k}_FILENAME`] = String(v.filename);
        transformed[`HONEY_FILE_${k}_SHA`] = String(v.sha);
      } else if (Array.isArray(v)) {
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

  const handleClearCache = () => {
    if (recipeName) {
      localStorage.removeItem(`honey_prompt_cache_${recipeName}`);
      message.success('Cache cleared. Loading defaults...');
      form.resetFields();
      form.setFieldsValue(getInitialValues());
    }
  };

  return (
    <Modal maskClosable={false}
      title={
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', paddingRight: 24 }}>
          <span>Recipe Parameters Required</span>
          {recipeName && (
            <Button size="small" type="text" icon={<ClearOutlined />} onClick={handleClearCache} title="Clear saved inputs">
              Reset to Defaults
            </Button>
          )}
        </div>
      }
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
          } else if (def.choices_url) {
            inputComponent = <RemoteSelect prompt={def} />;
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
          } else if (def.type === 'multiline') {
            inputComponent = <Input.TextArea rows={4} allowClear />;
          } else if (def.type === 'file') {
            inputComponent = <FileUpload />;
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
