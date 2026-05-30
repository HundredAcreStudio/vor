import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "./AppShell.tsx";
import { Repositories } from "./pages/Repositories.tsx";
import { RepoLayout } from "./pages/RepoLayout.tsx";
import { RepoOverview } from "./pages/repo/Overview.tsx";
import { Wiki } from "./pages/repo/Wiki.tsx";
import { Search } from "./pages/repo/Search.tsx";
import { RepoHealth } from "./pages/repo/Health.tsx";
import { RepoRisk } from "./pages/repo/Risk.tsx";
import { RepoHotspots } from "./pages/repo/Hotspots.tsx";
import { Graph } from "./pages/repo/Graph.tsx";
import { Symbols } from "./pages/repo/Symbols.tsx";
import { Contributors } from "./pages/repo/Contributors.tsx";
import { RepoDecisions } from "./pages/repo/Decisions.tsx";
import { RepoDeadCode } from "./pages/repo/DeadCode.tsx";
import { Dependencies } from "./pages/repo/Dependencies.tsx";
import { Coverage } from "./pages/repo/Coverage.tsx";
import { Costs } from "./pages/repo/Costs.tsx";
import { Activity } from "./pages/repo/Activity.tsx";
import { Security } from "./pages/repo/Security.tsx";
import { Settings as RepoSettings } from "./pages/repo/Settings.tsx";
import { Settings } from "./pages/Settings.tsx";
import "./styles.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <Routes>
        <Route element={<AppShell />}>
          <Route index element={<Navigate to="/repositories" replace />} />
          <Route path="repositories" element={<Repositories />} />
          <Route path="repositories/:repoId" element={<RepoLayout />}>
            <Route index element={<Navigate to="overview" replace />} />
            <Route path="overview" element={<RepoOverview />} />
            <Route path="wiki" element={<Wiki />} />
            <Route path="search" element={<Search />} />
            <Route path="risk" element={<RepoRisk />} />
            <Route path="health" element={<RepoHealth />} />
            <Route path="hotspots" element={<RepoHotspots />} />
            <Route path="graph" element={<Graph />} />
            <Route path="symbols" element={<Symbols />} />
            <Route path="dependencies" element={<Dependencies />} />
            <Route path="decisions" element={<RepoDecisions />} />
            <Route path="dead-code" element={<RepoDeadCode />} />
            <Route path="contributors" element={<Contributors />} />
            <Route path="coverage" element={<Coverage />} />
            <Route path="costs" element={<Costs />} />
            <Route path="security" element={<Security />} />
            <Route path="activity" element={<Activity />} />
            <Route path="settings" element={<RepoSettings />} />
          </Route>
          <Route path="settings" element={<Settings />} />
          <Route path="*" element={<Navigate to="/repositories" replace />} />
        </Route>
      </Routes>
    </BrowserRouter>
  </StrictMode>,
);
