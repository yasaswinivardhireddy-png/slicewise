import { Route, Routes } from 'react-router';
import { Layout } from './components/Layout.tsx';
import JobPage from './pages/JobPage.tsx';
import JobsPage from './pages/JobsPage.tsx';
import NewJobPage from './pages/NewJobPage.tsx';
import NotFoundPage from './pages/NotFoundPage.tsx';
import PodPage from './pages/PodPage.tsx';
import SimulatorPage from './pages/SimulatorPage.tsx';

export function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<PodPage />} />
        <Route path="jobs" element={<JobsPage />} />
        <Route path="jobs/new" element={<NewJobPage />} />
        <Route path="jobs/:id" element={<JobPage />} />
        <Route path="simulator" element={<SimulatorPage />} />
        <Route path="*" element={<NotFoundPage />} />
      </Route>
    </Routes>
  );
}
