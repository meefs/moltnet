import { Router } from "wouter";
import { Content } from "./components/Content";
import { MembersPanel } from "./components/Members";
import { Sidebar } from "./components/Sidebar";
import { StatusBar } from "./components/StatusBar";
import { TopBar } from "./components/TopBar";
import { EventStreamProvider, QueryProvider, SelectionProvider } from "./providers";

export function App() {
  return (
    <Router base="/console">
      <QueryProvider>
        <EventStreamProvider>
          <SelectionProvider>
            <TopBar />
            <main className="grid grid-cols-[248px_minmax(0,1fr)_auto] min-h-0 overflow-hidden">
              <Sidebar />
              <Content />
              <MembersPanel />
            </main>
            <StatusBar />
          </SelectionProvider>
        </EventStreamProvider>
      </QueryProvider>
    </Router>
  );
}
