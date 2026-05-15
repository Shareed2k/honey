declare module 'swagger-ui-react' {
  import type { ComponentType } from 'react';

  export type SwaggerRequest = {
    headers?: Record<string, string>;
    [key: string]: unknown;
  };

  export type SwaggerUIProps = {
    spec?: Record<string, unknown> | null;
    url?: string;
    deepLinking?: boolean;
    tryItOutEnabled?: boolean;
    docExpansion?: string;
    defaultModelsExpandDepth?: number;
    requestInterceptor?: (req: SwaggerRequest) => SwaggerRequest;
  };

  const SwaggerUI: ComponentType<SwaggerUIProps>;
  export default SwaggerUI;
}
