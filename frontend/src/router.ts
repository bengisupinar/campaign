import {
  createRouter,
  createWebHistory,
  type RouteLocationNormalized,
  type RouterHistory,
} from 'vue-router'

import AssistantView from '@/views/AssistantView.vue'
import CampaignEditView from '@/views/CampaignEditView.vue'
import CampaignsView from '@/views/CampaignsView.vue'
import CampaignView from '@/views/CampaignView.vue'

const id = (route: RouteLocationNormalized) => ({ id: Number(route.params.id) })

// createAppRouter returns the app's routes; tests pass an in-memory history.
export function createAppRouter(history: RouterHistory = createWebHistory()) {
  return createRouter({
    history,
    routes: [
      { path: '/', redirect: '/campaigns' },
      { path: '/campaigns', name: 'campaigns', component: CampaignsView },
      { path: '/campaigns/new', name: 'new', component: CampaignEditView },
      { path: '/campaigns/:id(\\d+)', name: 'campaign', component: CampaignView, props: id },
      { path: '/campaigns/:id(\\d+)/edit', name: 'edit', component: CampaignEditView, props: id },
      { path: '/assistant', name: 'assistant', component: AssistantView },
      { path: '/:unknown(.*)*', redirect: '/campaigns' },
    ],
  })
}
