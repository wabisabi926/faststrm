import { Routes, Route, Navigate } from "react-router-dom";
import { Toaster } from "@/components/ui/sonner";
import ClientAuthProvider from "@/components/ClientAuthProvider";
import LayoutWrapper from "@/components/LayoutWrapper";
import HomePage from "@/pages/home";
import LoginPage from "@/pages/login";
import AccountPage from "@/pages/account";
import TaskPage from "@/pages/task";
import SettingsPage from "@/pages/settings";
import HistoryPage from "@/pages/history";
import LifeEventsPage from "@/pages/life-events";
import AccountAlertsPage from "@/pages/account-alerts";
import TgNotifyPage from "@/pages/tg-notify";
import EmbyNotifyPage from "@/pages/emby-notify";
import LogDetailPage from "@/pages/log-detail";

export default function App() {
  return (
    <ClientAuthProvider>
      <LayoutWrapper>
        <Routes>
          <Route path="/" element={<HomePage />} />
          <Route path="/login" element={<LoginPage />} />
          <Route path="/account" element={<AccountPage />} />
          <Route path="/task" element={<TaskPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="/history" element={<HistoryPage />} />
          <Route path="/life-events" element={<LifeEventsPage />} />
          <Route path="/account-alerts" element={<AccountAlertsPage />} />
          <Route path="/tg-notify" element={<TgNotifyPage />} />
          <Route path="/emby-notify" element={<EmbyNotifyPage />} />
          <Route path="/notify" element={<Navigate to="/tg-notify" replace />} />
          <Route path="/notify/users" element={<Navigate to="/tg-notify" replace />} />
          <Route path="/log/:taskId" element={<LogDetailPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </LayoutWrapper>
      <Toaster />
    </ClientAuthProvider>
  );
}
