import { NavLink, Outlet } from "react-router-dom";
import { Icon } from "./Icon.tsx";

// The far-left icon rail. Each entry is a top-level area of the app. The
// contextual sidebar (repo menu / repo list) is rendered by the routed pages.
const RAIL: { to: string; icon: string; title: string }[] = [
  { to: "/repositories", icon: "folder", title: "Repositories" },
  { to: "/metrics", icon: "vital_signs", title: "Health Metrics" },
  { to: "/settings", icon: "settings", title: "Settings" },
];

/**
 * Top-level layout: the fixed icon rail plus an {@link Outlet} for the routed
 * page. Each rail entry links to a major app area; the contextual sidebar is
 * supplied by the routed pages themselves.
 */
export function AppShell() {
  return (
    <div className="shell">
      <nav className="rail">
        <div className="rail-brand" title="Vor">
          ⩕
        </div>
        {RAIL.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            title={item.title}
            className={({ isActive }) => "rail-icon" + (isActive ? " rail-icon-active" : "")}
          >
            <Icon name={item.icon} size={22} />
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}
