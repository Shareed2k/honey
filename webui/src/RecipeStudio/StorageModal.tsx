/* eslint-disable @typescript-eslint/no-explicit-any */
import React, { useEffect, useState } from 'react';
import { Modal, Radio, Form, Input, Button, message, Typography } from 'antd';

type Props = {
  visible: boolean;
  currentRecipeName?: string;
  onCancel: () => void;
  onSave: (options: {
    storage: string;
    path: string;
    commitMessage: string;
    gitUrl?: string;
    gitBranch?: string;
  }) => Promise<void>;
};

export default function StorageModal({ visible, currentRecipeName, onCancel, onSave }: Props) {
  const [form] = Form.useForm();
  const [storageType, setStorageType] = useState('local');
  const [saving, setSaving] = useState(false);
  const defaultRecipeName = currentRecipeName || 'visual-studio-recipe.cue';

  useEffect(() => {
    if (visible) {
      form.setFieldsValue({ path: defaultRecipeName });
    }
  }, [defaultRecipeName, form, visible]);

  const handleFinish = async (values: any) => {
    setSaving(true);
    try {
      await onSave({
        storage: storageType,
        path: values.path,
        commitMessage: storageType === 'git' ? values.commitMessage : '',
        gitUrl: values.gitUrl,
        gitBranch: values.gitBranch,
      });
      message.success('Recipe saved successfully!');
      onCancel();
    } catch (err: any) {
      message.error('Failed to save: ' + (err.message || err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal maskClosable={false}       title="Save Recipe Draft"
      open={visible}
      onCancel={onCancel}
      footer={null}
    >
      <Form
        form={form}
        layout="vertical"
        onFinish={handleFinish}
        initialValues={{
          path: defaultRecipeName,
          commitMessage: `Save recipe: ${defaultRecipeName}`,
          gitBranch: 'main',
        }}
      >
        <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
          Visual edits are saved as normalized JSON recipe content. Raw CUE formatting and comments are not preserved in this MVP.
        </Typography.Paragraph>

        <Form.Item label="Storage Destination">
          <Radio.Group value={storageType} onChange={(e) => setStorageType(e.target.value)}>
            <Radio.Button value="local">Local Directory</Radio.Button>
            <Radio.Button value="git">Git Repository</Radio.Button>
          </Radio.Group>
        </Form.Item>

        <Form.Item
          name="path"
          label={storageType === 'git' ? 'File path in Repository' : 'Recipe filename'}
          extra={storageType === 'local' ? 'Local recipe storage uses the filename only.' : undefined}
          rules={[
            { required: true, message: storageType === 'git' ? 'Please enter file path' : 'Please enter recipe filename' },
            {
              validator: (_, value) => {
                if (typeof value === 'string' && value.trim().toLowerCase().endsWith('.cue')) {
                  return Promise.resolve();
                }
                return Promise.reject(new Error('Recipe filename must end with .cue'));
              },
            }
          ]}
        >
          <Input />
        </Form.Item>

        {storageType === 'git' && (
          <>
            <Form.Item
              name="gitUrl"
              label="Git Repository Clone URL"
              rules={[{ required: true, message: 'Please enter clone URL' }]}
            >
              <Input placeholder="https://github.com/org/repo.git" />
            </Form.Item>
            <Form.Item name="gitBranch" label="Target Branch">
              <Input />
            </Form.Item>
          </>
        )}

        {storageType === 'git' && (
          <Form.Item
            name="commitMessage"
            label="Save / Commit Message"
            rules={[{ required: true, message: 'Please enter commit message' }]}
          >
            <Input.TextArea rows={3} />
          </Form.Item>
        )}

        <Form.Item style={{ textAlign: 'right', marginBottom: 0 }}>
          <Button style={{ marginRight: 8 }} onClick={onCancel}>
            Cancel
          </Button>
          <Button type="primary" htmlType="submit" loading={saving}>
            Save Recipe
          </Button>
        </Form.Item>
      </Form>
    </Modal>
  );
}
