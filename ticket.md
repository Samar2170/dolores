#####
* So we have to get the parsed annual report and parsed investor presentation from archivus client.
* Also get ['payload']['companyProfile']['officers'] if available from indiasm_stock collection.
* Fetch ['payload']['shareholding'] from indiasm_stock collection.
* Get report from company_business_and_competitive_analysis collection for the company symbol.
* Get report from company_financial_analysis collection for the company symbol.

* Pass all the data and prompt from @prompts/management_analysis.md to agent and get the report and save it to a new company_management_analysis collection.
