import React, { useEffect, useState } from 'react';
import { Modal, Card, Typography, Spin, Collapse } from 'antd';
import { fetchLibraryRecipes, type LibraryCategory, type LibraryRecipe } from '../api';

const { Text } = Typography;

type Props = {
  open: boolean;
  onCancel: () => void;
  onSelect: (recipe: LibraryRecipe) => void;
};

export function LibraryModal({ open, onCancel, onSelect }: Props) {
  const [categories, setCategories] = useState<LibraryCategory[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (open) {
      setLoading(true);
      fetchLibraryRecipes()
        .then(setCategories)
        .finally(() => setLoading(false));
    }
  }, [open]);

  return (
    <Modal
      title="Recipe Library"
      open={open}
      onCancel={onCancel}
      footer={null}
      width={800}
      styles={{ body: { maxHeight: '70vh', overflowY: 'auto' } }}
    >
      {loading ? (
        <div style={{ textAlign: 'center', padding: '2rem' }}>
          <Spin />
        </div>
      ) : categories.length === 0 ? (
        <Text type="secondary">No recipes found in the library.</Text>
      ) : (
        <Collapse
          defaultActiveKey={categories.map(c => c.name)}
          ghost
          items={categories.map(cat => ({
            key: cat.name,
            label: <Text strong>{cat.name} ({cat.recipes.length})</Text>,
            children: (
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                {cat.recipes.map(recipe => (
                  <Card
                    key={recipe.filename}
                    size="small"
                    hoverable
                    onClick={() => onSelect(recipe)}
                    style={{ background: '#141922', borderColor: '#2a3140' }}
                  >
                    <div style={{ fontWeight: 600, color: '#e8e8e8' }}>{recipe.name}</div>
                    <div style={{ fontSize: '0.8rem', color: '#8b949e', marginTop: 4 }}>
                      {recipe.description.slice(0, 100)}{recipe.description.length > 100 ? '...' : ''}
                    </div>
                  </Card>
                ))}
              </div>
            )
          }))}
        />
      )}
    </Modal>
  );
}