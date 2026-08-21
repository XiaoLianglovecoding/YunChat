export type ApiEnvelope<T> = {
  code: string;
  message: string;
  data?: T;
  request_id: string;
};

export type ApiErrorDetails = {
  status: number;
  code: string;
  message: string;
  requestId: string;
};

