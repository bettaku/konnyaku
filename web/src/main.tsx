/* @refresh reload */
import { render } from "solid-js/web";
import { HashRouter, Route } from "@solidjs/router";
import "./style.css";
import { App } from "./App";
import { ProjectsPage } from "./pages/Projects";
import { ProjectPage } from "./pages/Project";
import { GlossaryPage } from "./pages/Glossary";
import { ComponentPage } from "./pages/Component";
import { RepositoryPage } from "./pages/Repository";
import { LocalesPage } from "./pages/Locales";
import { UsersPage } from "./pages/Users";
import { DeliveriesPage } from "./pages/Deliveries";

render(
  () => (
    <HashRouter root={App}>
      <Route path="/" component={ProjectsPage} />
      <Route path="/projects" component={ProjectsPage} />
      <Route path="/projects/:id" component={ProjectPage} />
      <Route path="/projects/:id/glossary" component={GlossaryPage} />
      <Route path="/components/:id" component={ComponentPage} />
      <Route path="/repositories/:id" component={RepositoryPage} />
      <Route path="/locales" component={LocalesPage} />
      <Route path="/users" component={UsersPage} />
      <Route path="/deliveries" component={DeliveriesPage} />
      <Route path="*" component={ProjectsPage} />
    </HashRouter>
  ),
  document.getElementById("root")!,
);
