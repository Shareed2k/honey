import React, { useEffect } from 'react';
import { Modal, Form, Input } from 'antd';

export type PromptDef = {
  description?: string;
  type?: string;
  required?: boolean;
};

type Props = {
  open: boolean;
  prompts: Record<string, PromptDef>;
  onCancel: () => void;
  onSubmit: (values: Record<string, string>) => void;
};

export function ParameterPromptModal({ open, prompts, onCancel, onSubmit }: Props) {
  const [form] = Form.useForm();

  useEffect(() => {
    if (open) form.resetFields();
  }, [open, form]);

  return (
    <Modal
      title="Recipe Parameters Required"
      open={open}
      onCancel={onCancel}
      onOk={() => form.submit()}
      okText="Continue"
    >
      <Form form={form} layout="vertical" onFinish={onSubmit}>
        {Object.entries(prompts).map(([key, def]) => (
          <Form.Item
            key={key}
            name={key}
            label={key}
            rules={[{ required: def.required, message: `Please input ${key}` }]}
            tooltip={def.description}
          >
            {def.type === 'password' ? <Input.Password /> : <Input />}
          </Form.Item>
        ))}
      </Form>
    </Modal>
  );
}
