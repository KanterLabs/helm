// App-layer services handed to the membership components. Keeping them as
// props lets lib/ components stay independent of the application shell.
export type ConfirmRequest = { title: string; message: string; confirmLabel: string };
export type ConfirmAction = (request: ConfirmRequest) => Promise<boolean>;
