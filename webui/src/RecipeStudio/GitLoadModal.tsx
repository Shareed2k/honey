import React, { useEffect, useState } from 'react';
import { Modal, Form, Input, Button, message } from 'antd';
import { apiGet } from '../api';

type Props = {
  visible: boolean;
  onCancel: () => void;
  onLoad: (options: {
    gitUrl: string;
    gitBranch: string;
    path: string;
    gitUser: string;
    gitPass: string;
    gitSsh: string;
  }) => Promise<void>;
};

export default function GitLoadModal({ visible, onCancel, onLoad }: Props) {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [configLoaded, setConfigLoaded] = useState(false);

  useEffect(() => {
    if (visible && !configLoaded) {
      apiGet('/api/v1/recipes/studio-config')
        .then((res) => res.json())
        .then((data) => {
          form.setFieldsValue({
            gitUrl: data.git_url || '',
            gitBranch: data.git_branch || 'main',
            gitUser: data.git_user || '',
            gitPass: data.git_pass_configured ? '••••••••' : '',
            gitSsh: data.git_ssh_configured ? '••••••••' : '',
          });
          setConfigLoaded(true);
        })
        .catch((err) => message.error('Failed to load defaults: ' + err.message));
    }
  }, [visible, configLoaded, form]);

  const handleFinish = async (values: any) => {
    setLoading(true);
    try {
      await onLoad({
        gitUrl: values.gitUrl,
        gitBranch: values.gitBranch,
        path: values.path,
        gitUser: values.gitUser,
        gitPass: values.gitPass,
        gitSsh: values.gitSsh,
      });
      onCancel();
    } catch (err: any) {
      // Error handled by parent
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal
      title="Load Recipe from Git"
      open={visible}
      onCancel={onCancel}
      footer={null}
    >
      <Form
        form={form}
        layout="vertical"
        onFinish={handleFinish}
      >
        <Form.Item
          name="gitUrl"
          label="Git Repository Clone URL"
          rules={[{ required: true, message: 'Please enter clone URL' }]}
        >
          <Input placeholder="https://github.com/org/repo.git" disabled={loading} />
        </Form.Item>

        <Form.Item
          name="gitBranch"
          label="Target Branch"
          rules={[{ required: true, message: 'Please enter target branch' }]}
        >
          <Input placeholder="main" disabled={loading} />
        </Form.Item>

        <Form.Item
          name="path"
          label="Recipe filename/path in Repo"
          rules={[
            { required: true, message: 'Please enter recipe file path' },
            {
              validator: (_, value) => {
                if (typeof value === 'string' && value.trim().toLowerCase().endsWith('.cue')) {
                  return Promise.resolve();
                }
                return Promise.reject(new Error('Recipe path must end with .cue'));
              },
            },
          ]}
        >
          <Input placeholder="recipes/install.cue" disabled={loading} />
        </Form.Item>

        <Form.Item name="gitUser" label="Git Username">
          <Input placeholder="Optional username" disabled={loading} />
        </Form.Item>

        <Form.Item name="gitPass" label="Git Password/Token">
          <Input.Password placeholder="Optional password or token" disabled={loading} />
        </Form.Item>

        <Form.Item name="gitSsh" label="Git SSH Private Key">
          <Input.TextArea rows={4} placeholder="Optional SSH private key" disabled={loading} />
        </Form.Item>

        <Form.Item style={{ textAlign: 'right', marginBottom: 0 }}>
          <Button style={{ marginRight: 8 }} onClick={onCancel}>
            Cancel
          </Button>
          <Button type="primary" htmlType="submit" loading={loading}>
            Load Recipe
          </Button>
        </Form.Item>
      </Form>
    </Modal>
  );
}
