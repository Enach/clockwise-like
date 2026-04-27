// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
	integrations: [
		starlight({
			title: 'Paceday Help',
			description: 'Learn how to get more out of your calendar with Paceday.',
			logo: {
				src: './src/assets/logo.svg',
				alt: 'Paceday',
			},
			favicon: '/favicon.ico',
			customCss: ['./src/styles/custom.css'],
			sidebar: [
				{
					label: 'Getting started',
					items: [
						{ label: 'What is Paceday?', slug: 'getting-started/what-is-paceday' },
						{ label: 'Connecting your calendar', slug: 'getting-started/connecting-your-calendar' },
						{ label: 'Choosing your profile', slug: 'getting-started/choosing-your-profile' },
						{ label: 'Demo mode', slug: 'getting-started/demo-mode' },
					],
				},
				{
					label: 'Your calendar',
					items: [
						{ label: 'Reading your calendar', slug: 'calendar/reading-your-calendar' },
						{ label: 'Creating and editing events', slug: 'calendar/creating-and-editing-events' },
						{ label: 'Meeting prep briefs', slug: 'calendar/meeting-prep-briefs' },
					],
				},
				{
					label: 'Focus time',
					items: [
						{ label: 'What is focus time?', slug: 'focus/what-is-focus-time' },
						{ label: 'Buffer time', slug: 'focus/buffer-time' },
						{ label: 'Analytics', slug: 'focus/analytics' },
					],
				},
				{
					label: 'Habits',
					items: [
						{ label: 'What are habits?', slug: 'habits/what-are-habits' },
						{ label: 'Creating a habit', slug: 'habits/creating-a-habit' },
						{ label: 'Streaks and completion', slug: 'habits/streaks-and-completion' },
					],
				},
				{
					label: 'Scheduling links',
					items: [
						{ label: 'What are scheduling links?', slug: 'scheduling/what-are-scheduling-links' },
						{ label: 'Creating a link', slug: 'scheduling/creating-a-link' },
						{ label: 'Collective scheduling', slug: 'scheduling/collective-scheduling' },
						{ label: 'Participant availability', slug: 'scheduling/participant-availability' },
					],
				},
				{
					label: 'My Team',
					items: [
						{ label: 'Getting started as a manager', slug: 'team/getting-started-as-a-manager' },
						{ label: '1:1 cadences', slug: 'team/one-on-one-cadences' },
						{ label: 'Team member focus analytics', slug: 'team/team-member-focus-analytics' },
						{ label: 'Protected hours', slug: 'team/protected-hours' },
						{ label: 'Find a time', slug: 'team/find-a-time' },
					],
				},
				{
					label: 'Integrations',
					items: [
						{ label: 'Slack', slug: 'integrations/slack' },
						{ label: 'Notion', slug: 'integrations/notion' },
						{ label: 'Google Calendar', slug: 'integrations/google-calendar' },
						{ label: 'Microsoft Outlook', slug: 'integrations/microsoft-outlook' },
					],
				},
				{
					label: 'Settings',
					items: [
						{ label: 'Profile', slug: 'settings/profile' },
						{ label: 'Notifications', slug: 'settings/notifications' },
						{ label: 'Buffer time settings', slug: 'settings/buffer-time-settings' },
						{ label: 'Analytics privacy', slug: 'settings/analytics-privacy' },
					],
				},
				{
					label: 'Troubleshooting',
					items: [
						{ label: 'Calendar not syncing', slug: 'help/calendar-not-syncing' },
						{ label: 'Briefs not showing', slug: 'help/briefs-not-showing' },
						{ label: 'Habits not scheduling', slug: 'help/habits-not-scheduling' },
						{ label: 'FAQ', slug: 'help/faq' },
					],
				},
			],
		}),
	],
});
