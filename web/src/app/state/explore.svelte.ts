// Reactive state: Roadmap explore mode (the roadmap alone, enlarged).
class ExploreState {
  // Mirrors `?explore=1` on a roadmap URL; App applies it only on the roadmap.
  active = $state.raw(false);
  zoom = $state.raw(1.15);
  trigger = $state.raw<HTMLButtonElement | null>(null);
  exitButton = $state.raw<HTMLButtonElement | null>(null);
}

export const explore = new ExploreState();
