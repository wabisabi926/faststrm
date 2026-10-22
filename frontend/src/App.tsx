import { Routes, Route, Navigate } from "react-router-dom";
import { Toaster } from "@/components/ui/sonner";
import ClientAuthProvider from "@/components/ClientAuthProvider";
import PasswordChangeGate from "@/components/PasswordChangeGate";
import SetupGate from "@/components/SetupGate";
import LayoutWrapper from "@/components/LayoutWrapper";
import HomePage from "@/pages/home";
import LoginPage from "@/pages/login";
import AccountPage from "@/pages/account";
import TaskPage from "@/pages/task";
import SettingsPage from "@/pages/settings";
import HistoryPage from "@/pages/history";
import LifeEventsHubPage from "@/pages/life-events-hub";
import NotificationCenterPage from "@/pages/notification-center";
import LogDetailPage from "@/pages/log-detail";
import AccountSecurityPage from "@/pages/account-security";
import CleanupPage from "@/pages/cleanup";
import DiagnosePage from "@/pages/diagnose";
import RuntimePage from "@/pages/runtime";

export default function App() {
  return (
    <SetupGate>
      <ClientAuthProvider>
        <PasswordChangeGate>
          <LayoutWrapper>
            <Routes>
              <Route path="/" element={<HomePage />} />
              <Route path="/login" element={<LoginPage />} />
              <Route path="/account" element={<AccountPage />} />
              <Route path="/task" element={<TaskPage />} />
              <Route path="/settings" element={<SettingsPage />} />
              <Route path="/settings/monitor" element={<SettingsPage section="monitor" />} />
              <Route path="/cleanup" element={<CleanupPage />} />
              <Route path="/diagnose" element={<DiagnosePage />} />
              <Route path="/runtime" element={<RuntimePage />} />
              <Route path="/history" element={<HistoryPage />} />
              <Route path="/life-events" element={<LifeEventsHubPage />} />
              <Route path="/account-alerts" element={<Navigate to="/account" replace />} />
              <Route path="/notify" element={<NotificationCenterPage />} />
              <Route path="/tg-notify" element={<Navigate to="/notify?tab=telegram" replace />} />
              <Route path="/emby-notify" element={<Navigate to="/notify?tab=emby" replace />} />
              <Route path="/account-security" element={<AccountSecurityPage />} />
              <Route path="/notify/users" element={<Navigate to="/notify?tab=telegram" replace />} />
              <Route path="/log/:taskId" element={<LogDetailPage />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </LayoutWrapper>
        </PasswordChangeGate>
      </ClientAuthProvider>
      <Toaster />
    </SetupGate>
  );
}
