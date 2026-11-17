export interface PullRequestLink {
  id: string;
  task_id: string;
  provider: 'github' | 'gitea';
  host: string;
  repository: string;
  number: number;
  url: string;
  created_at: string;
}
