export interface JobSendOptions {
  singletonKey?: string;
  startAfterSeconds?: number;
}

export interface JobPort {
  send(name: string, data: unknown, opts?: JobSendOptions): Promise<string>;
  work(name: string, handler: (data: unknown) => Promise<void>): Promise<void>;
  schedule(name: string, cron: string): Promise<void>;
  close(): Promise<void>;
}
