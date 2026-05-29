import { NavLink, Outlet } from "react-router-dom";

// The far-left icon rail. Each entry is a top-level area of the app. The
// contextual sidebar (repo menu / repo list) is rendered by the routed pages.
const RAIL: { to: string; icon: string; title: string }[] = [
  { to: "/repositories", icon: "▤", title: "Repositories" },
  { to: "/settings", icon: "⚙", title: "Settings" },
];

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
            {item.icon}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}
