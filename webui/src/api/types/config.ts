

export type ConfigSchemaFieldType = 'string' | 'boolean' | 'integer' | 'array' | 'object';

export type ConfigSchemaFieldSpec = {
  key: string;
  label: string;
  type: ConfigSchemaFieldType;
  required?: boolean;
  secret?: boolean;
  enum?: string[];
  enum_as_warning?: boolean;
  default?: unknown;
  items?: ConfigSchemaFieldSpec[];
};

export type ConfigBackendSchema = {
  label: string;
  fields: ConfigSchemaFieldSpec[];
};

export type ConfigUISchema = {
  top_level_keys: string[];
  defaults: ConfigSchemaFieldSpec[];
  backends: Record<string, ConfigBackendSchema>;
  backend_order: string[];
};

export type ConfigSchemaResponse = {
  json_schema: Record<string, unknown>;
  ui_schema: ConfigUISchema;
};