import { createContext, useContext } from "react";
import type { User } from "./types";

/** The signed-in person, for the screens that need to know who it is. */
export interface AuthValue {
  user: User;
  /** After a change to your own account (an admin renaming herself), so the sidebar shows it. */
  setUser: (user: User) => void;
  /** Ends this session (POST /api/auth/logout) and shows the login page. */
  signOut: () => Promise<void>;
}

export const AuthContext = createContext<AuthValue>(null!);

export function useAuth() {
  return useContext(AuthContext);
}
